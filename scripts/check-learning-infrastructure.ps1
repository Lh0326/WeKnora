#Requires -Version 7.0
param([switch]$StartContainers)
$ErrorActionPreference = 'Stop'
$learningRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $learningRoot

# Environment A uses Docker Desktop, independently of the WSL deployment.
# Bound daemon calls so a failed Desktop startup cannot hang the launcher.
function Invoke-LearningDocker {
    param([string[]]$DockerArguments, [int]$TimeoutSeconds = 15)
    $learningInfo = [Diagnostics.ProcessStartInfo]::new((Get-Command docker -ErrorAction Stop).Source)
    $learningInfo.UseShellExecute = $false
    $learningInfo.CreateNoWindow = $true
    $learningInfo.RedirectStandardOutput = $true
    $learningInfo.RedirectStandardError = $true
    foreach ($learningArg in @('--context', 'desktop-linux') + $DockerArguments) {
        $learningInfo.ArgumentList.Add($learningArg)
    }
    $learningCommand = [Diagnostics.Process]::Start($learningInfo)
    try {
        $learningOutput = $learningCommand.StandardOutput.ReadToEndAsync()
        $learningError = $learningCommand.StandardError.ReadToEndAsync()
        if (!$learningCommand.WaitForExit($TimeoutSeconds * 1000)) {
            $learningCommand.Kill()
            throw 'Docker did not respond within the startup timeout.'
        }
        if ($learningCommand.ExitCode -ne 0) {
            throw $learningError.GetAwaiter().GetResult().Trim()
        }
        return $learningOutput.GetAwaiter().GetResult().Trim()
    } finally { $learningCommand.Dispose() }
}

try {
    $null = Invoke-LearningDocker -DockerArguments @('info', '--format', '{{.ServerVersion}}')
} catch {
    throw "Hybrid infrastructure is unavailable: Docker Desktop's Linux engine is not ready. See hls/WeKnora启动说明书.md. No backend was stopped or built; the WSL deployment uses a different database. Docker detail: $($_.Exception.Message)"
}
if ($StartContainers) {
    # Reuse the development volumes and existing containers; do not rebuild or pull images.
    Invoke-LearningDocker -DockerArguments @('compose', '-f', 'docker-compose.dev.yml', '--profile', 'neo4j', 'up', '-d', '--no-recreate', '--pull', 'never', '--wait', '--wait-timeout', '45', 'postgres', 'redis', 'docreader', 'neo4j') -TimeoutSeconds 55
}
foreach ($learningContainer in @('WeKnora-postgres-dev', 'WeKnora-redis-dev', 'WeKnora-docreader-dev', 'WeKnora-neo4j-dev')) {
    $learningState = Invoke-LearningDocker -DockerArguments @('inspect', '--format', '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}}', $learningContainer)
    if ($learningState -notmatch '^running(?: healthy)?$') {
        throw "$learningContainer is not ready ($learningState). Run scripts/check-learning-infrastructure.ps1 -StartContainers before restarting the backend."
    }
}
foreach ($learningPort in @(5432, 6379, 50051, 7687)) {
    $learningConnection = [Net.Sockets.TcpClient]::new()
    try {
        if (!$learningConnection.ConnectAsync('127.0.0.1', $learningPort).Wait(2000)) { throw "Port $learningPort did not respond." }
    } catch {
        throw "Hybrid infrastructure port $learningPort is unavailable. Check the development containers and port mappings."
    } finally { $learningConnection.Dispose() }
}
Write-Output 'Hybrid infrastructure ready: Docker Desktop development containers (environment A).'
