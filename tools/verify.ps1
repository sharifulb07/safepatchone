# Run from any directory. Native tools must be installed; failures are fatal.
# -Postgres requires a disposable test database configured through libpq PG* vars.
[CmdletBinding()]
param([switch]$Postgres)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$npmCommand = if ($env:OS -eq 'Windows_NT') { 'npm.cmd' } else { 'npm' }
$checkResults = [System.Collections.Generic.List[object]]::new()
$reportDirectory = Join-Path $repoRoot 'dist/validation'
New-Item -ItemType Directory -Force -Path $reportDirectory | Out-Null
$previousCache = $env:GOCACHE
$previousPostgres = $env:SAFEPATCH_POSTGRES_MIGRATION_TEST
$env:GOCACHE = Join-Path ([System.IO.Path]::GetTempPath()) 'safepatch-production-review-cache'
$env:SAFEPATCH_POSTGRES_MIGRATION_TEST = $null

function Invoke-Check {
    param([string]$Name, [string]$Executable, [string[]]$Arguments)
    Write-Host "Validating $Name"
    $started = [DateTime]::UtcNow
    & $Executable @Arguments
    $resultCode = $LASTEXITCODE
    $checkResults.Add([pscustomobject]@{
        name = $Name
        status = if ($resultCode -eq 0) { 'passed' } else { 'failed' }
        exit_code = $resultCode
        seconds = [Math]::Round(([DateTime]::UtcNow - $started).TotalSeconds, 2)
    })
    if ($resultCode -ne 0) { throw "$Name failed with exit code $resultCode" }
}

Push-Location $repoRoot
$validationPassed = $false
try {
    Invoke-Check 'Go module integrity' 'go' @('mod', 'verify')
    Invoke-Check 'Go tests' 'go' @('test', './...', '-count=1')
    Invoke-Check 'Go race tests' 'go' @('test', '-race', './...', '-count=1')
    Invoke-Check 'Go vet' 'go' @('vet', './...')
    Invoke-Check 'Go vulnerability scan' 'go' @('run', 'golang.org/x/vuln/cmd/govulncheck@v1.8.0', './...')
    Invoke-Check 'Command builds' 'go' @('build', '-o', 'bin/', './cmd/...')
    Invoke-Check 'CLI version' (Join-Path $repoRoot $(if ($env:OS -eq 'Windows_NT') { 'bin/safepatch.exe' } else { 'bin/safepatch' })) @('version', '--json')
    if ($Postgres) {
        $env:SAFEPATCH_POSTGRES_MIGRATION_TEST = '1'
        Invoke-Check 'Live PostgreSQL migrations' 'go' @('test', './internal/platform/migrationtest', '-run', 'TestPostgres', '-count=1', '-v')
    } else {
        $checkResults.Add([pscustomobject]@{ name = 'Live PostgreSQL migrations'; status = 'not_run'; reason = 'Requires -Postgres and a disposable PG* database' })
    }
    Push-Location (Join-Path $repoRoot 'web')
    try {
        Invoke-Check 'Locked web dependencies' $npmCommand @('ci')
        Invoke-Check 'Live npm vulnerability audit' $npmCommand @('audit', '--audit-level=high')
        Invoke-Check 'Production web build' $npmCommand @('run', 'build')
        Invoke-Check 'TypeScript' $npmCommand @('run', 'typecheck')
        Invoke-Check 'Production browser tests' $npmCommand @('run', 'test:e2e')
    } finally { Pop-Location }
    Invoke-Check 'Whitespace check' 'git' @('-c', 'core.safecrlf=false', 'diff', '--check')
    $validationPassed = $true
} finally {
    [pscustomobject]@{
        completed_at = [DateTime]::UtcNow.ToString('o')
        local_validation_passed = $validationPassed
        production_readiness = 'not_certified'
        checks = @($checkResults.ToArray())
    } | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $reportDirectory 'report.json') -Encoding UTF8
    $env:GOCACHE = $previousCache
    $env:SAFEPATCH_POSTGRES_MIGRATION_TEST = $previousPostgres
    Pop-Location
}
Write-Host 'Local validation passed. Production acceptance remains required; see docs/releases/2026-10-03-review.md.'
