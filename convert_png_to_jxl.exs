#!/usr/bin/env elixir

defmodule ConvertPngToJxl.Stamp do
  @moduledoc false
  use GenServer

  @reconnect_limit 5

  def start_link do
    GenServer.start_link(__MODULE__, nil, name: __MODULE__)
  end

  def copy_times(from, to) do
    try do
      GenServer.call(__MODULE__, {:copy_times, from, to}, :infinity)
    catch
      :exit, _ -> fallback_copy(from, to)
    end
  end

  def fallback_copy(from, to) do
    opts = [:raw, {:time, :posix}]

    case :file.read_file_info(from, opts) do
      {:ok, info} -> :file.write_file_info(to, info, opts)
      error -> error
    end
  rescue
    e -> {:error, Exception.message(e)}
  end

  @impl true
  def init(_arg) do
    Process.flag(:trap_exit, true)
    {:ok, ensure_port(%{port: nil, buf: "", crashes: 0})}
  end

  @impl true
  def handle_call({:copy_times, from, to}, _from, state) do
    state = ensure_port(state)

    case state.port do
      nil ->
        {:reply, fallback_copy(from, to), state}

      port ->
        stamp_via_port(port, from, to, state)
    end
  end

  @impl true
  def handle_info({port, {:data, data}}, %{port: port, buf: buf} = state) do
    {:noreply, %{state | buf: buf <> data}}
  end

  def handle_info({port, {:exit_status, _}}, %{port: port} = state) do
    {:noreply, drop_dead_port(state)}
  end

  def handle_info({:EXIT, port, _}, %{port: port} = state) do
    {:noreply, drop_dead_port(state)}
  end

  def handle_info(_msg, state), do: {:noreply, state}

  @impl true
  def terminate(_reason, %{port: port}) when is_port(port) do
    close_port(port)
    :ok
  end

  def terminate(_reason, _state), do: :ok

  defp stamp_via_port(port, from, to, state) do
    buf = discard_complete_lines(state.buf)
    line = quote_ps(from) <> "\t" <> quote_ps(to) <> "\n"

    if Port.command(port, line) do
      case await_reply(port, buf) do
        {:ok, reply, buf} ->
          case parse_reply(reply) do
            :ok ->
              {:reply, :ok, %{state | buf: buf, crashes: 0}}

            {:error, _} ->
              {:reply, fallback_copy(from, to), %{state | buf: buf}}
          end

        {:error, _} ->
          close_port(port)
          {:reply, fallback_copy(from, to), drop_dead_port(state)}
      end
    else
      close_port(port)
      {:reply, fallback_copy(from, to), drop_dead_port(state)}
    end
  end

  defp ensure_port(%{port: port} = state) when is_port(port), do: state

  defp ensure_port(%{crashes: n} = state) when n >= @reconnect_limit do
    %{state | port: nil}
  end

  defp ensure_port(state) do
    case open_port() do
      {:ok, port} ->
        %{state | port: port, buf: ""}

      :not_found ->
        %{state | port: nil, crashes: @reconnect_limit}

      :error ->
        %{state | port: nil, crashes: state.crashes + 1}
    end
  end

  defp drop_dead_port(state) do
    %{state | port: nil, buf: "", crashes: state.crashes + 1}
  end

  defp open_port do
    case find_powershell() do
      nil ->
        :not_found

      exe ->
        try do
          port =
            Port.open({:spawn_executable, String.to_charlist(exe)}, [
              :binary,
              :exit_status,
              :hide,
              :use_stdio,
              args: ["-NoProfile", "-NoLogo", "-NonInteractive", "-Command", ps_command()]
            ])

          {:ok, port}
        rescue
          _ -> :error
        catch
          _, _ -> :error
        end
    end
  end

  defp find_powershell do
    names = ["powershell.exe", "pwsh.exe", "pwsh", "powershell"]

    case Enum.find_value(names, &System.find_executable/1) do
      path when is_binary(path) ->
        path

      _ ->
        root = System.get_env("SystemRoot") || System.get_env("SYSTEMROOT")

        extras =
          if is_binary(root) do
            [
              Path.join([root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"]),
              Path.join([root, "System32", "powershell.exe"])
            ]
          else
            []
          end

        Enum.find(extras, &File.regular?/1)
    end
  end

  defp ps_command do
    ~S"""
    $ErrorActionPreference = 'Continue'
    $utf8 = New-Object System.Text.UTF8Encoding $false
    [Console]::OutputEncoding = $utf8
    $stdin = New-Object IO.StreamReader([Console]::OpenStandardInput(), $utf8)
    function Decode-Path([string]$p) {
      if ($p.Length -ge 2 -and $p.StartsWith("'") -and $p.EndsWith("'")) {
        return $p.Substring(1, $p.Length - 2).Replace("''", "'")
      }
      return $p
    }
    function Write-Reply([string]$text) {
      [Console]::Out.WriteLine($text)
      [Console]::Out.Flush()
    }
    while ($true) {
      $line = $stdin.ReadLine()
      if ($null -eq $line) { break }
      try {
        $idx = $line.IndexOf([char]9)
        if ($idx -lt 0) { Write-Reply 'ERR bad line'; continue }
        $from = Decode-Path $line.Substring(0, $idx)
        $to = Decode-Path $line.Substring($idx + 1)
        $src = Get-Item -LiteralPath $from
        $dst = Get-Item -LiteralPath $to
        $dst.CreationTimeUtc = $src.CreationTimeUtc
        $dst.LastWriteTimeUtc = $src.LastWriteTimeUtc
        Write-Reply 'OK'
      } catch {
        $msg = ([string]$_.Exception.Message) -replace '[\r\n]+', ' '
        Write-Reply ('ERR ' + $msg)
      }
    }
    """
  end

  defp quote_ps(path) do
    "'" <> String.replace(path, "'", "''") <> "'"
  end

  defp parse_reply(line) do
    case String.trim(line) do
      "OK" -> :ok
      "OK" <> _ -> :ok
      "ERR" <> rest -> {:error, String.trim(rest)}
      other -> {:error, other}
    end
  end

  defp await_reply(port, buf) do
    await_reply(port, buf, System.monotonic_time(:millisecond) + 20_000)
  end

  defp await_reply(port, buf, deadline) do
    case take_line(buf) do
      {:ok, "", rest} ->
        await_reply(port, rest, deadline)

      {:ok, line, rest} ->
        {:ok, line, rest}

      :incomplete ->
        now = System.monotonic_time(:millisecond)
        wait = max(deadline - now, 0)

        if wait == 0 do
          {:error, :timeout}
        else
          receive do
            {^port, {:data, data}} ->
              await_reply(port, buf <> data, deadline)

            {^port, {:exit_status, status}} ->
              {:error, {:exit_status, status}}

            {:EXIT, ^port, reason} ->
              {:error, reason}
          after
            wait ->
              {:error, :timeout}
          end
        end
    end
  end

  defp discard_complete_lines(buf) do
    case take_line(buf) do
      {:ok, _line, rest} -> discard_complete_lines(rest)
      :incomplete -> buf
    end
  end

  defp take_line(buf) do
    case :binary.split(buf, "\n") do
      [line, rest] -> {:ok, String.trim_trailing(line, "\r"), rest}
      [_] -> :incomplete
    end
  end

  defp close_port(port) do
    try do
      Port.close(port)
    rescue
      _ -> :ok
    catch
      _, _ -> :ok
    end
  end
end

defmodule ConvertPngToJxl do
  @moduledoc false

  alias ConvertPngToJxl.Stamp

  @stats __MODULE__.Stats
  @log __MODULE__.Log
  @log_name "convert-png-to-jxl.log"
  @gib 1_073_741_824
  @stop_key {__MODULE__, :stop}

  @strict [
    path: :string,
    effort: :integer,
    distance: :float,
    lossless: :boolean,
    workers: :integer,
    threads_per_worker: :integer,
    keep_originals: :boolean,
    limit: :integer,
    skip_newer_than_seconds: :integer
  ]

  def main(argv) do
    Process.flag(:trap_exit, true)
    :persistent_term.put(@stop_key, false)
    trap_shutdown()

    try do
      run(argv)
    rescue
      e ->
        IO.puts(:stderr, Exception.message(e))
        try_footer()
        halt!(1)
    catch
      :exit, {:halt, _} ->
        :ok

      :exit, reason ->
        IO.puts(:stderr, "exit: #{inspect(reason)}")
        try_footer()
        halt!(1)
    end
  end

  def request_stop, do: :persistent_term.put(@stop_key, true)
  def stopped?, do: :persistent_term.get(@stop_key, false)

  defp run(argv) do
    opts = parse_args!(argv)
    script_dir = Path.expand(__DIR__)
    log_path = Path.join(script_dir, @log_name)
    cjxl = required_command!("cjxl")
    jxlinfo = required_command!("jxlinfo")
    folder = resolve_folder(opts.path, script_dir)

    {:ok, _} =
      Agent.start_link(
        fn ->
          %{converted: 0, failed: 0, skipped: 0, bytes_in: 0, bytes_out: 0}
        end,
        name: @stats
      )

    {:ok, _} = Agent.start_link(fn -> log_path end, name: @log)
    {:ok, _} = Stamp.start_link()

    {empty_count, pngs} = scan_folder(folder)
    {already, work} = partition_already_jxl(pngs, jxlinfo, opts.keep_originals)

    work =
      if opts.limit > 0 do
        Enum.take(work, opts.limit)
      else
        work
      end

    mode =
      if opts.lossless do
        "lossless -d 0 -e #{opts.effort}"
      else
        "visually-lossy -d #{fmt_num(opts.distance)} -e #{opts.effort}"
      end

    stamp =
      NaiveDateTime.local_now()
      |> NaiveDateTime.truncate(:second)
      |> NaiveDateTime.to_iso8601()

    emit("=== #{stamp} convert PNG -> JXL ===")
    emit("folder=#{folder}")

    emit(
      "mode=#{mode} workers=#{opts.workers} threads/worker=#{opts.threads} keepOriginals=#{fmt_bool(opts.keep_originals)}"
    )

    emit("pending=#{length(work)} alreadyHadJxl=#{already} emptyPngsLeftAlone=#{empty_count}")

    cond do
      work == [] ->
        IO.puts("Nothing to convert.")
        halt!(0)

      stopped?() ->
        finish()

      true ->
        ctx = %{
          cjxl: cjxl,
          jxlinfo: jxlinfo,
          effort: opts.effort,
          distance: opts.distance,
          threads: opts.threads,
          keep_originals: opts.keep_originals,
          skip_newer_than_seconds: opts.skip_newer_than_seconds
        }

        run_work(work, ctx, opts.workers)
        finish()
    end
  end

  defp parse_args!(argv) do
    case OptionParser.parse(argv, strict: @strict) do
      {opts, [], []} ->
        build_opts(opts)

      {_opts, extra, []} ->
        die("unexpected arguments: #{Enum.join(extra, ", ")}")

      {_opts, _, invalid} ->
        pretty =
          Enum.map_join(invalid, ", ", fn
            {opt, nil} -> opt
            {opt, val} -> "#{opt} #{val}"
          end)

        die("invalid options: #{pretty}")
    end
  end

  defp build_opts(opts) do
    effort = Keyword.get(opts, :effort, 7)
    distance = Keyword.get(opts, :distance, 1.0) * 1.0
    workers = Keyword.get(opts, :workers, 8)
    threads = Keyword.get(opts, :threads_per_worker, 3)
    limit = Keyword.get(opts, :limit, 0)
    skip = Keyword.get(opts, :skip_newer_than_seconds, 30)
    lossless = Keyword.get(opts, :lossless, false)
    keep = Keyword.get(opts, :keep_originals, false)

    range!(effort, 1, 10, "--effort")
    range!(workers, 1, 32, "--workers")
    range!(threads, 0, 64, "--threads-per-worker")

    if distance < 0.0 or distance > 25.0 do
      die("--distance must be in 0..25")
    end

    if not is_integer(limit) or limit < 0 do
      die("--limit must be >= 0")
    end

    if not is_integer(skip) or skip < 0 do
      die("--skip-newer-than-seconds must be >= 0")
    end

    %{
      path: Keyword.get(opts, :path),
      effort: effort,
      distance: if(lossless, do: 0.0, else: distance),
      lossless: lossless,
      workers: workers,
      threads: threads,
      keep_originals: keep,
      limit: limit,
      skip_newer_than_seconds: skip
    }
  end

  defp range!(val, min, max, name) do
    unless is_integer(val) and val >= min and val <= max do
      die("#{name} must be in #{min}..#{max}")
    end
  end

  defp required_command!(name) do
    case System.find_executable(name) do
      nil -> die("Required command '#{name}' is not on PATH.")
      path -> path
    end
  end

  defp resolve_folder(path, script_dir) do
    folder =
      if blank_path?(path) do
        Path.dirname(script_dir)
      else
        Path.expand(path)
      end

    case File.stat(folder) do
      {:ok, %{type: :directory}} -> folder
      {:ok, _} -> die("not a directory: #{folder}")
      {:error, _} -> die("Path not found: #{folder}")
    end
  end

  defp blank_path?(path) when path in [nil, ""], do: true
  defp blank_path?(path) when is_binary(path), do: String.trim(path) == ""
  defp blank_path?(_), do: true

  defp scan_folder(folder) do
    {empty, pngs} =
      Enum.reduce(File.ls!(folder), {0, []}, fn name, {empty, pngs} ->
        path = Path.join(folder, name)
        down = String.downcase(name)

        cond do
          String.ends_with?(down, ".jxl.partial") ->
            _ = File.rm(path)
            {empty, pngs}

          String.ends_with?(down, ".png") ->
            case File.stat(path) do
              {:ok, %{type: :regular, size: 0}} ->
                {empty + 1, pngs}

              {:ok, %{type: :regular, size: size}} when size > 0 ->
                {empty, [{name, path} | pngs]}

              _ ->
                {empty, pngs}
            end

          true ->
            {empty, pngs}
        end
      end)

    {empty, Enum.sort_by(pngs, &elem(&1, 0))}
  end

  defp partition_already_jxl(pngs, jxlinfo, keep_originals) do
    {already, work} =
      Enum.reduce_while(pngs, {0, []}, fn {_name, path}, {already, work} ->
        if stopped?() do
          {:halt, {already, work}}
        else
          dest = jxl_path(path)

          case verify_jxl(dest, jxlinfo) do
            :ok ->
              case Stamp.copy_times(path, dest) do
                :ok -> unless keep_originals, do: File.rm(path)
                {:error, _} -> :ok
              end

              {:cont, {already + 1, work}}

            {:error, _} ->
              {:cont, {already, [path | work]}}
          end
        end
      end)

    {already, Enum.reverse(work)}
  end

  defp run_work(work, ctx, workers) do
    total = length(work)
    started = System.monotonic_time(:millisecond)

    job =
      Task.async(fn ->
        Process.flag(:trap_exit, true)

        Task.async_stream(
          work,
          fn png -> convert_one(png, ctx) end,
          max_concurrency: workers,
          timeout: :infinity,
          ordered: false
        )
        |> Enum.each(fn
          {:ok, _} ->
            :ok

          {:exit, reason} ->
            bump(:failed)
            log("WORKER-ERROR\t#{inspect(reason)}")
        end)
      end)

    await_progress(job, started, total)
  end

  defp await_progress(job, started, total) do
    Process.sleep(5_000)
    print_progress(started, total)

    case Task.yield(job, 0) do
      nil ->
        await_progress(job, started, total)

      {:ok, _} ->
        :ok

      {:exit, reason} ->
        log("WORKER-ERROR\t#{inspect(reason)}")
        bump(:failed)
    end
  end

  defp convert_one(png_path, ctx) do
    if stopped?() do
      :stopped
    else
      do_convert_one(png_path, ctx)
    end
  end

  defp do_convert_one(png_path, ctx) do
    name = Path.basename(png_path)
    dest = jxl_path(png_path)
    partial = dest <> ".partial"

    try do
      %{size: size, mtime: mtime} = File.stat!(png_path, time: :posix)

      cond do
        System.os_time(:second) - mtime < ctx.skip_newer_than_seconds ->
          bump(:skipped)
          log("SKIP-RECENT\t#{name}")
          :skipped

        match?(:ok, verify_jxl(dest, ctx.jxlinfo)) ->
          _ = Stamp.copy_times(png_path, dest)
          unless ctx.keep_originals, do: File.rm(png_path)
          out_size = file_size(dest)
          bump(:converted, size, out_size)
          log("OK-EXISTING\t#{name}\t#{size}\t#{out_size}")
          :ok

        stopped?() ->
          :stopped

        true ->
          encode_one(png_path, name, dest, partial, size, ctx)
      end
    rescue
      e ->
        _ = File.rm(partial)
        bump(:failed)
        log("FAIL\t#{png_path}\t#{Exception.message(e)}")
        :failed
    end
  end

  defp encode_one(png_path, name, dest, partial, size, ctx) do
    _ = File.rm(partial)

    args = [
      png_path,
      partial,
      "-d",
      fmt_num(ctx.distance),
      "-e",
      Integer.to_string(ctx.effort),
      "--num_threads",
      Integer.to_string(ctx.threads),
      "--quiet"
    ]

    {cjxl_out, cjxl_status} = System.cmd(ctx.cjxl, args, stderr_to_stdout: true)
    verify = verify_jxl(partial, ctx.jxlinfo)

    case {cjxl_status, verify} do
      {0, :ok} ->
        _ = File.rm(dest)

        case File.rename(partial, dest) do
          :ok ->
            :ok

          {:error, _} ->
            _ = File.rm(dest)

            case File.rename(partial, dest) do
              :ok -> :ok
              {:error, reason} -> raise "rename failed: #{inspect(reason)}"
            end
        end

        _ = Stamp.copy_times(png_path, dest)
        unless ctx.keep_originals, do: File.rm(png_path)
        out_size = file_size(dest)
        bump(:converted, size, out_size)
        log("OK\t#{name}\t#{size}\t#{out_size}")
        :ok

      {status, verify} ->
        _ = File.rm(partial)
        bump(:failed)
        log("FAIL\t#{name}\t#{fail_detail(cjxl_out, status, verify)}")
        :failed
    end
  end

  defp verify_jxl(path, jxlinfo) do
    cond do
      not File.regular?(path) ->
        {:error, "missing"}

      file_size(path) <= 32 ->
        {:error, "too small"}

      true ->
        {out, status} = System.cmd(jxlinfo, [path], stderr_to_stdout: true)

        if status == 0 do
          :ok
        else
          text = one_line(out)

          {:error,
           "jxlinfo exit #{status}" <>
             if(text == "", do: "", else: " #{text}")}
        end
    end
  end

  defp fail_detail(cjxl_out, status, verify) do
    cjxl_part =
      case {status, one_line(cjxl_out)} do
        {0, ""} -> nil
        {0, text} -> "cjxl #{text}"
        {s, ""} -> "cjxl exit #{s}"
        {s, text} -> "cjxl exit #{s} #{text}"
      end

    info_part =
      case verify do
        :ok -> nil
        {:error, msg} -> one_line(to_string(msg))
      end

    case Enum.reject([cjxl_part, info_part], &(&1 in [nil, ""])) do
      [] -> "encode/verify failed"
      parts -> Enum.join(parts, " | ")
    end
  end

  defp jxl_path(png_path), do: Path.rootname(png_path) <> ".jxl"

  defp file_size(path) do
    case File.stat(path) do
      {:ok, %{size: size}} -> size
      _ -> 0
    end
  end

  defp bump(field, bytes_in \\ 0, bytes_out \\ 0) do
    try do
      Agent.update(@stats, fn s ->
        %{
          s
          | converted: s.converted + if(field == :converted, do: 1, else: 0),
            failed: s.failed + if(field == :failed, do: 1, else: 0),
            skipped: s.skipped + if(field == :skipped, do: 1, else: 0),
            bytes_in: s.bytes_in + bytes_in,
            bytes_out: s.bytes_out + bytes_out
        }
      end)
    catch
      :exit, _ -> :ok
    end
  end

  defp emit(line) do
    IO.puts(line)
    log(line)
  end

  defp log(line) do
    try do
      Agent.update(@log, fn path ->
        try do
          _ = File.write(path, [line, ?\n], [:append])
        rescue
          _ -> :ok
        end

        path
      end)
    catch
      :exit, _ -> :ok
    end
  end

  defp snapshot do
    try do
      Agent.get(@stats, & &1)
    catch
      :exit, _ ->
        %{converted: 0, failed: 0, skipped: 0, bytes_in: 0, bytes_out: 0}
    end
  end

  defp print_progress(started_ms, total) do
    s = snapshot()
    done = s.converted + s.failed + s.skipped
    elapsed_ms = max(System.monotonic_time(:millisecond) - started_ms, 0)
    elapsed_s = elapsed_ms / 1000
    rate = if elapsed_s > 0, do: done / elapsed_s, else: 0.0
    remaining = max(total - done, 0)
    eta_ms = if rate > 0, do: round(remaining / rate * 1000), else: 0
    saved = :erlang.float_to_binary((s.bytes_in - s.bytes_out) / @gib, decimals: 2)

    IO.puts(
      "#{done}/#{total}  ok=#{s.converted} fail=#{s.failed} skip=#{s.skipped}  saved=#{saved} GiB  elapsed=#{hms(elapsed_ms)}  eta=#{hms(eta_ms)}"
    )
  end

  defp hms(ms) do
    sec = div(max(ms, 0), 1000)
    h = div(sec, 3600)
    m = div(rem(sec, 3600), 60)
    s = rem(sec, 60)
    pad = fn n -> n |> Integer.to_string() |> String.pad_leading(2, "0") end
    "#{pad.(h)}:#{pad.(m)}:#{pad.(s)}"
  end

  defp finish do
    s = snapshot()

    emit(
      "=== done converted=#{s.converted} failed=#{s.failed} skipped=#{s.skipped} savedBytes=#{s.bytes_in - s.bytes_out} ==="
    )

    halt!(if(s.failed > 0, do: 1, else: 0))
  end

  defp try_footer do
    try do
      s = snapshot()

      emit(
        "=== done converted=#{s.converted} failed=#{s.failed} skipped=#{s.skipped} savedBytes=#{s.bytes_in - s.bytes_out} ==="
      )
    catch
      _, _ -> :ok
    end
  end

  defp trap_shutdown do
    handler = fn ->
      request_stop()
      :ok
    end

    Enum.each([:sigterm, :sighup, :sigquit], fn sig ->
      _ = System.trap_signal(sig, handler)
    end)
  end

  defp fmt_num(n) when is_integer(n), do: Integer.to_string(n)

  defp fmt_num(n) when is_float(n) do
    if n == trunc(n) do
      Integer.to_string(trunc(n))
    else
      Float.to_string(n)
    end
  end

  defp fmt_bool(true), do: "True"
  defp fmt_bool(false), do: "False"

  defp one_line(s) do
    s
    |> to_string()
    |> String.replace(~r/[\r\n]+/, " ")
    |> String.trim()
  end

  defp die(msg) do
    IO.puts(:stderr, msg)
    halt!(1)
  end

  defp halt!(code) do
    stop_named(Stamp, fn pid -> GenServer.stop(pid, :normal, 5_000) end)
    stop_named(@stats, &Agent.stop/1)
    stop_named(@log, &Agent.stop/1)
    System.halt(code)
  end

  defp stop_named(name, fun) do
    if pid = Process.whereis(name) do
      try do
        fun.(pid)
      catch
        :exit, _ -> :ok
      end
    end
  end
end

ConvertPngToJxl.main(System.argv())
