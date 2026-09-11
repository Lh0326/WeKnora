#Requires -Version 7.0
param([switch]$SkipBuild)
$ErrorActionPreference = 'Stop'
$learningRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $learningRoot
& (Join-Path $PSScriptRoot 'check-learning-infrastructure.ps1')
$learningCompiler = Split-Path (Get-Command gcc).Source
$env:PATH = "$learningCompiler;$env:PATH"
$env:CGO_CFLAGS = "-I$($learningRoot.Replace('\','/'))/.local-data/cgo-include -Wno-deprecated-declarations -Wno-gnu-folding-constant"
$learningStamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$learningExe = Join-Path $learningRoot ".local-data/weknora-learning-$learningStamp.exe"
if ($SkipBuild) {
    $learningLatest = Get-ChildItem -LiteralPath (Join-Path $learningRoot '.local-data') -Filter 'weknora-learning-*.exe' | Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if (!$learningLatest) { throw 'No previously built learning backend is available.' }
    $learningExe = $learningLatest.FullName
}
if (!$SkipBuild) {
    # Build a separate binary first; failed builds leave the running server intact.
    go build -ldflags '-X google.golang.org/protobuf/reflect/protoregistry.conflictPolicy=warn' -o $learningExe ./cmd/server
    if ($LASTEXITCODE -ne 0) { throw 'Backend build failed; the running server has not been stopped.' }
}
if (!(Test-Path -LiteralPath $learningExe)) { throw 'Backend executable is missing.' }
foreach ($learningEnvFile in @('.env','.env.local')) {
    if (!(Test-Path -LiteralPath $learningEnvFile)) { continue }
    foreach ($learningLine in Get-Content -LiteralPath $learningEnvFile) {
        if ($learningLine -match '^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=(.*)$') {
            [Environment]::SetEnvironmentVariable($Matches[1], $Matches[2].Trim().Trim('"').Trim("'"), 'Process')
        }
    }
}
$env:DB_HOST='127.0.0.1'
$env:REDIS_ADDR='127.0.0.1:6379'
$env:DOCREADER_ADDR='127.0.0.1:50051'
$env:DOCREADER_TRANSPORT='grpc'
$env:NEO4J_URI='bolt://127.0.0.1:7687'
$env:LOCAL_STORAGE_BASE_DIR=Join-Path $learningRoot '.local-data/files'
$env:SERVER_HOST='127.0.0.1'
$env:SERVER_PORT='18081'
$env:LEARNING_ENABLE='true'
$env:AUTO_MIGRATE='true'
$learningListeners = @(Get-NetTCPConnection -State Listen -LocalPort 18081 -ErrorAction SilentlyContinue)
foreach ($learningProcessId in ($learningListeners.OwningProcess | Select-Object -Unique)) {
    $learningProcess = Get-Process -Id $learningProcessId
    $learningProcessPath = [IO.Path]::GetFullPath($learningProcess.Path)
    $learningRuntimeDir = [IO.Path]::GetFullPath((Join-Path $learningRoot '.local-data')) + [IO.Path]::DirectorySeparatorChar
    if (!$learningProcessPath.StartsWith($learningRuntimeDir,[StringComparison]::OrdinalIgnoreCase) -or $learningProcess.ProcessName -notlike '*server*' -and $learningProcess.ProcessName -notlike 'weknora-learning*') {
        throw 'Port 18081 is owned by another application; no process has been stopped.'
    }
    Stop-Process -Id $learningProcessId
    $learningProcess.WaitForExit(10000) | Out-Null
}
$learningLogDir = Join-Path $learningRoot '.local-data/logs'
New-Item -ItemType Directory -Path $learningLogDir -Force | Out-Null
$learningStamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$learningStarted = Start-Process -FilePath $learningExe -WorkingDirectory $learningRoot -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $learningLogDir "learning-$learningStamp.log") -RedirectStandardError (Join-Path $learningLogDir "learning-$learningStamp.err.log")
for ($learningAttempt=0; $learningAttempt -lt 40; $learningAttempt++) {
    if ($learningStarted.HasExited) { throw "Backend exited; inspect .local-data/logs/learning-$learningStamp.*.log" }
    try {
        $learningHealth = Invoke-RestMethod 'http://127.0.0.1:18081/health' -TimeoutSec 2
        if ($learningHealth.status -eq 'ok') { Write-Output "Learning backend ready on 18081 (PID $($learningStarted.Id))."; exit 0 }
    } catch { Start-Sleep -Seconds 1 }
}
throw 'Backend health check did not become ready; inspect the runtime logs.'
