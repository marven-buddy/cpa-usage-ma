param(
    [ValidateSet('Build', 'Test', 'Start', 'Stop', 'Status', 'Smoke')]
    [string]$Action = 'Status'
)
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path $PSScriptRoot -Parent
$toolsRoot = Join-Path (Split-Path $repoRoot -Parent) '.tools'
$devRoot = Join-Path $repoRoot '.dev'
$statePath = Join-Path $devRoot 'processes.json'
$nodeExe = (Get-Command node -ErrorAction Stop).Source
$goExe = Join-Path $toolsRoot 'go/bin/go.exe'
if (!(Test-Path $goExe)) { $goExe = (Get-Command go -ErrorAction Stop).Source }
$compilerDir = Join-Path $toolsRoot 'w64devkit/bin'
if (Test-Path $compilerDir) {
    $env:PATH = "$compilerDir;$env:PATH"
    $env:CC = Join-Path $compilerDir 'gcc.exe'
}
$env:CGO_ENABLED = '1'
$env:TZ = 'Asia/Shanghai'
New-Item -ItemType Directory -Force $devRoot | Out-Null

function Invoke-Checked([string]$Program, [string[]]$Arguments) {
    & $Program @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Command failed ($LASTEXITCODE): $Program $Arguments" }
}

function Build-Plugin {
    $outDir = Join-Path $devRoot 'plugins/windows/amd64'
    New-Item -ItemType Directory -Force $outDir | Out-Null
    $version = (Get-Content (Join-Path $repoRoot 'VERSION') -Raw).Trim()
    Invoke-Checked $goExe @('-C', (Join-Path $repoRoot 'plugin'), 'build', '-buildmode=c-shared', '-ldflags', "-X main.version=$version-dev", '-o', (Join-Path $outDir 'usage-report.dll'), '.')
}

function Get-DevState {
    if (Test-Path $statePath) { return Get-Content $statePath -Raw | ConvertFrom-Json }
    return @()
}

function Stop-Dev {
    foreach ($entry in (Get-DevState)) {
        $process = Get-Process -Id $entry.id -ErrorAction SilentlyContinue
        if ($process -and $process.StartTime.ToUniversalTime().Ticks -eq ([datetime]$entry.started).ToUniversalTime().Ticks) {
            Stop-Process -Id $entry.id -ErrorAction Stop
        }
    }
    if (Test-Path $statePath) { Remove-Item -LiteralPath $statePath }
}

function Wait-URL([string]$URL) {
    for ($attempt = 0; $attempt -lt 60; $attempt++) {
        try { return Invoke-RestMethod -Uri $URL -TimeoutSec 2 -NoProxy } catch { Start-Sleep -Milliseconds 500 }
    }
    throw "Service did not start: $URL. See .dev/*.log"
}

function Read-Key {
    return (Get-Content (Join-Path $devRoot 'management-key') -Raw).Trim()
}

switch ($Action) {
    'Build' { Build-Plugin }
    'Test' {
        Invoke-Checked $goExe @('-C', (Join-Path $repoRoot 'plugin'), 'test', './...')
        $frontendTests = @(Get-ChildItem -LiteralPath (Join-Path $repoRoot 'frontend/src') -Filter '*.test.js' | Select-Object -ExpandProperty FullName)
        Invoke-Checked $nodeExe (@('--test', '--test-isolation=none') + $frontendTests)
    }
    'Stop' { Stop-Dev; Write-Host 'Development services stopped.' }
    'Status' {
        foreach ($entry in (Get-DevState)) {
            $process = Get-Process -Id $entry.id -ErrorAction SilentlyContinue
            [pscustomobject]@{ Service = $entry.name; PID = $entry.id; Running = [bool]($process -and $process.StartTime.ToUniversalTime().Ticks -eq ([datetime]$entry.started).ToUniversalTime().Ticks) }
        }
    }
    'Start' {
        if (@(Get-DevState).Count -gt 0) { throw 'Run -Action Stop before starting again.' }
        foreach ($port in 18317, 18318, 15173) {
            $client = [Net.Sockets.TcpClient]::new()
            try { $client.Connect('127.0.0.1', $port); throw "Port $port is already in use." }
            catch [Net.Sockets.SocketException] { }
            finally { $client.Dispose() }
        }
        Build-Plugin
        $cpaExe = Join-Path $toolsRoot 'cpa-v8.0.48/cli-proxy-api.exe'
        if (!(Test-Path $cpaExe)) { throw "CPA v8 development host missing: $cpaExe" }
        $keyPath = Join-Path $devRoot 'management-key'
        if (!(Test-Path $keyPath)) { [guid]::NewGuid().ToString('N') | Set-Content $keyPath -NoNewline }
        $key = Read-Key
        $plugins = (Join-Path $devRoot 'plugins').Replace('\', '/')
        $database = (Join-Path $devRoot 'data/usage.sqlite').Replace('\', '/')
        $auth = (Join-Path $devRoot 'auths').Replace('\', '/')
        @"
config-version: 8
server:
  host: 127.0.0.1
  port: 18317
management:
  allow-remote: false
  secret-key: '$key'
  disable-control-panel: true
access:
  api-keys: [local-dev-client, local-dev-client-b]
oauth:
  auth-dir: '$auth'
api-keys:
  openai-compatibility:
    - name: local-dev
      base-url: http://127.0.0.1:18318/v1
      keys:
        - api-key: local-dev-upstream
          proxy-url: direct
      models:
        - name: gpt-4o-mini
observability:
  usage:
    usage-statistics-enabled: false
plugins:
  enabled: true
  dir: '$plugins'
  configs:
    usage-report:
      enabled: true
      db_path: '$database'
"@ | Set-Content (Join-Path $devRoot 'config.yaml') -Encoding utf8
        $env:CPA_DEV_TARGET = 'http://127.0.0.1:18317'
        $env:VITE_CPA_DEV_KEY = $key
        $entries = [Collections.Generic.List[object]]::new()
        function Launch([string]$Name, [string]$Program, [string]$Arguments, [string]$Directory) {
            $process = Start-Process -FilePath $Program -ArgumentList $Arguments -WorkingDirectory $Directory -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $devRoot "$Name.log") -RedirectStandardError (Join-Path $devRoot "$Name.error.log")
            $entries.Add(@{ name = $Name; id = $process.Id; started = $process.StartTime.ToUniversalTime().ToString('o') })
            ConvertTo-Json -InputObject @($entries.ToArray()) | Set-Content $statePath
        }
        try {
            Launch 'upstream' $nodeExe ('"' + (Join-Path $PSScriptRoot 'mock-upstream.mjs') + '"') $repoRoot
            $null = Wait-URL 'http://127.0.0.1:18318/health'
            Launch 'cpa' $cpaExe ('--config "' + (Join-Path $devRoot 'config.yaml') + '" --local-model') $devRoot
            $null = Wait-URL 'http://127.0.0.1:18317/'
            Launch 'frontend' $nodeExe 'node_modules/vite/bin/vite.js --host 127.0.0.1 --port 15173 --strictPort' (Join-Path $repoRoot 'frontend')
            $null = Wait-URL 'http://127.0.0.1:15173/'
            Write-Host 'Dashboard: http://127.0.0.1:15173'
            Write-Host 'CPA v8:   http://127.0.0.1:18317'
            Write-Host 'Run -Action Smoke to generate local fixture traffic.'
        } catch { Stop-Dev; throw }
    }
    'Smoke' {
        $base = 'http://127.0.0.1:18317'
        $today = Get-Date -Format 'yyyy-MM-dd'
        $api = "$base/v0/management/plugins/usage-report/api/usage?start=$today&end=$today&bucket=hour"
        $authHeaders = @{ Authorization = "Bearer $(Read-Key)" }
        $before = Invoke-RestMethod -Uri $api -Headers $authHeaders -NoProxy
        $beforeCount = @($before.events).Count
        $headers = @{ Authorization = 'Bearer local-dev-client' }
        foreach ($stream in $false, $true) {
            $body = @{ model = 'gpt-4o-mini'; stream = $stream; messages = @(@{ role = 'user'; content = 'Local development smoke test' }) } | ConvertTo-Json -Depth 5
            $result = Invoke-WebRequest -Uri "$base/v1/chat/completions" -Method Post -Headers $headers -Body $body -ContentType 'application/json' -TimeoutSec 20 -NoProxy
            if ($result.StatusCode -ne 200) { throw 'Model execution failed.' }
            if ($stream -and $result.Content -notmatch '\[DONE\]') { throw 'Streaming response has no terminal marker.' }
        }
        $secondBody = @{ model = 'gpt-4o-mini'; messages = @(@{ role = 'user'; content = 'Second client attribution fixture' }) } | ConvertTo-Json -Depth 5
        $secondResult = Invoke-WebRequest -Uri "$base/v1/chat/completions" -Method Post -Headers @{ Authorization = 'Bearer local-dev-client-b' } -Body $secondBody -ContentType 'application/json' -TimeoutSec 20 -NoProxy
        if ($secondResult.StatusCode -ne 200) { throw 'Second client execution failed.' }
        $failure = @{ model = 'gpt-4o-mini'; messages = @(@{ role = 'user'; content = '[fail]' }) } | ConvertTo-Json -Depth 5
        $rejected = Invoke-WebRequest -Uri "$base/v1/chat/completions" -Method Post -Headers $headers -Body $failure -ContentType 'application/json' -SkipHttpErrorCheck -TimeoutSec 20 -NoProxy
        if ($rejected.StatusCode -ne 400) { throw "Expected fixture failure 400, got $($rejected.StatusCode)" }
        $unauthorized = Invoke-WebRequest -Uri $api -SkipHttpErrorCheck -NoProxy
        if ($unauthorized.StatusCode -notin 401,403) { throw 'Statistics API did not require authentication.' }
        for ($attempt = 0; $attempt -lt 40; $attempt++) {
            $data = Invoke-RestMethod -Uri $api -Headers $authHeaders -NoProxy
            $success = @($data.events | Where-Object { !$_.failed -and $_.input -eq 100 -and $_.output -eq 20 -and $_.cache -eq 40 })
            $failures = @($data.events | Where-Object failed)
            if (@($data.events).Count -ge $beforeCount + 4 -and $success.Count -ge 3 -and $failures.Count -ge 1) { break }
            Start-Sleep -Milliseconds 250
        }
        if (@($data.events).Count -lt $beforeCount + 4 -or $success.Count -lt 3 -or $failures.Count -lt 1 -or $data.collect_err) { throw 'Fixture traffic was not persisted correctly.' }
        $newEvents = @($data.events | Select-Object -Skip $beforeCount)
        $clientGroups = @($newEvents | Group-Object key)
        if ($newEvents.Count -ne 4 -or @($newEvents | Where-Object { $_.key_kind -ne 'client' -or !$_.upstream_id }).Count -ne 0 -or $clientGroups.Count -ne 2 -or (@($clientGroups | ForEach-Object { $_.Count } | Sort-Object) -join ',') -ne '1,3') {
            throw 'Two clients sharing one upstream were not attributed independently.'
        }
        [pscustomobject]@{ Version = $data.version; Events = @($data.events).Count; NewEvents = $newEvents.Count; ClientGroups = $clientGroups.Count; SuccessfulFixtures = $success.Count; FailedFixtures = $failures.Count; AuthRequired = $true; Database = (Join-Path $devRoot 'data/usage.sqlite') }
    }
}
