[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('up', 'down', 'stop', 'restart', 'ps', 'status', 'logs', 'migrate', 'rebuild', 'test', 'verify', 'debug', 'clean', 'e2e', 'e2e-clean')]
    [string]$Command = 'up',

    [Parameter(Position = 1, ValueFromRemainingArguments = $true)]
    [string[]]$Services = @(),

    [ValidateRange(1024, 65535)]
    [int]$DebugPort = 12345
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$composeFile = Join-Path $repoRoot 'compose.dev.yaml'
$toolsComposeFile = Join-Path $repoRoot 'deploy/development/compose.tools.yaml'
$devEnv = Join-Path $repoRoot '.env.development'
$exampleEnv = Join-Path $repoRoot '.env.example'

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    throw 'Docker CLI is not available. Start Docker Desktop and try again.'
}

if ($Command -notin @('e2e', 'e2e-clean') -and -not (Test-Path -LiteralPath $devEnv)) {
    Copy-Item -LiteralPath $exampleEnv -Destination $devEnv
    Write-Host "Created isolated development settings: $devEnv"
}

function Invoke-DevCompose {
    param([Parameter(Mandatory = $true)][string[]]$Arguments)

    $composeArguments = @('--env-file', $devEnv, '-f', $composeFile)
    if ($Arguments[0] -eq 'run') { $composeArguments += @('-f', $toolsComposeFile) }
    & docker compose @composeArguments @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "docker compose failed with exit code $LASTEXITCODE"
    }
}

function Get-DevPublishedAddress {
    param(
        [Parameter(Mandatory = $true)][string]$Service,
        [Parameter(Mandatory = $true)][int]$ContainerPort
    )

    $address = (& docker compose --env-file $devEnv -f $composeFile port $Service $ContainerPort).Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($address)) {
        throw "Cannot resolve the published port for $Service`:$ContainerPort"
    }
    return $address
}

function Assert-DevDiskBudget {
    param([int]$LimitGiB = 20, [switch]$WarnOnly)

    Write-Host 'Docker disk usage:'
    & docker system df
    if ($LASTEXITCODE -ne 0) { throw 'Cannot read Docker disk usage.' }
    $root = docker info --format '{{.DockerRootDir}}'
    if ($root) {
        Write-Host "Docker root: $root"
    }
    foreach ($vhdx in @(
            (Join-Path $env:LOCALAPPDATA 'Docker\wsl\disk\docker_data.vhdx'),
            (Join-Path $env:LOCALAPPDATA 'Docker\wsl\data\ext4.vhdx')
        )) {
        if (Test-Path -LiteralPath $vhdx) {
            $len = (Get-Item -LiteralPath $vhdx).Length
            Write-Host ("Host virtual disk {0}: {1:N2} GiB" -f $vhdx, ($len / 1GB))
        }
    }
    $bytes = 0
    $df = docker system df --format '{{json .}}'
    if ($LASTEXITCODE -ne 0) { throw 'Cannot read Docker disk usage.' }
    foreach ($line in $df) {
        $entry = $line | ConvertFrom-Json
        $bytes += ConvertTo-ByteCount $entry.Size
    }
    $usedGiB = [math]::Round($bytes / 1GB, 2)
    Write-Host "All-project Docker usage about $usedGiB GiB (management threshold $LimitGiB GiB)."
    if ($usedGiB -ge $LimitGiB) {
        $message = "All-project Docker usage $usedGiB GiB is at or above the $LimitGiB GiB management threshold. Review cache usage before starting a heavy task."
        if ($WarnOnly) { Write-Warning $message } else { throw $message }
    }
}

function ConvertTo-ByteCount {
    param([string]$Text)
    if ($Text.Trim() -notmatch '^([0-9]+(?:\.[0-9]+)?)\s*(B|[KMGT]i?B)?$') {
        throw "Unrecognized Docker size: $Text"
    }
    $number = [double]::Parse($Matches[1], [System.Globalization.CultureInfo]::InvariantCulture)
    $unit = [string]$Matches[2]
    $factor = switch ($unit.ToUpperInvariant()) {
        'KB'  { 1000 }; 'MB'  { 1000000 }; 'GB'  { 1000000000 }; 'TB'  { 1000000000000 }
        'KIB' { 1KB }; 'MIB' { 1MB }; 'GIB' { 1GB }; 'TIB' { 1TB }
        default { 1 }
    }
    return [int64]($number * $factor)
}

function Invoke-DevGoTests {
    # This runner owns fresh E2E state and never starts consumers against it.
    & node (Join-Path $repoRoot 'e2e/run.mjs') backend
    if ($LASTEXITCODE -ne 0) { throw "Isolated Go verification failed with exit code $LASTEXITCODE" }
}

function Invoke-DevTestSuite {
    Invoke-DevGoTests
    Invoke-DevCompose -Arguments @('run', '--rm', '--no-deps', 'frontend', 'npm', 'test')
}

function Invoke-DevStaticChecks {
    Invoke-DevCompose -Arguments @('run', '--rm', '--no-deps', 'frontend', 'npm', 'run', 'lint')
    Invoke-DevCompose -Arguments @('run', '--rm', '--no-deps', 'frontend', 'npm', 'run', 'build')
}

Push-Location $repoRoot
try {
    switch ($Command) {
        'up' {
            Assert-DevDiskBudget
            Invoke-DevCompose -Arguments (@('up', '-d', '--wait', '--wait-timeout', '600', '--remove-orphans') + $Services)
            $frontendAddress = Get-DevPublishedAddress -Service 'frontend' -ContainerPort 5173
            $apiAddress = Get-DevPublishedAddress -Service 'api' -ContainerPort 8080
            Write-Host 'EasyGPA Plus development environment is ready:'
            Write-Host "  frontend  http://$frontendAddress"
            Write-Host "  API       http://$apiAddress/healthz"
            Write-Host 'Use scripts/dev.ps1 logs to follow container output.'
        }
        'down' {
            Invoke-DevCompose -Arguments @('down', '--remove-orphans')
            Write-Host 'Containers stopped; dependency caches and development data were preserved.'
        }
        'stop' {
            Invoke-DevCompose -Arguments (@('stop') + $Services)
        }
        'restart' {
            if ($Services.Count -eq 0) {
                Invoke-DevCompose -Arguments @('restart', 'api', 'workers', 'frontend')
            } else {
                Invoke-DevCompose -Arguments (@('restart') + $Services)
            }
        }
        'ps' {
            Invoke-DevCompose -Arguments @('ps')
        }
        'status' {
            Invoke-DevCompose -Arguments @('ps')
            Assert-DevDiskBudget -WarnOnly
        }
        'logs' {
            & docker compose --env-file $devEnv -f $composeFile logs --tail 200 -f @Services
        }
        'migrate' {
            Invoke-DevCompose -Arguments @('run', '--rm', 'migrate')
        }
        'rebuild' {
            Assert-DevDiskBudget
            Invoke-DevCompose -Arguments @('build', '--pull', 'migrate', 'api', 'workers')
            Invoke-DevCompose -Arguments @('up', '-d', '--wait', '--wait-timeout', '600', '--remove-orphans')
        }
        'test' {
            Assert-DevDiskBudget
            Invoke-DevTestSuite
        }
        'e2e' {
            Assert-DevDiskBudget
            & node (Join-Path $repoRoot 'e2e/run.mjs') all
            if ($LASTEXITCODE -ne 0) {
                throw "E2E suite failed with exit code $LASTEXITCODE"
            }
        }
        'e2e-clean' {
            & node (Join-Path $repoRoot 'e2e/run.mjs') clean
            if ($LASTEXITCODE -ne 0) {
                throw "E2E cleanup failed with exit code $LASTEXITCODE"
            }
        }
        'debug' {
            $target = if ($Services.Count -gt 0) { $Services[0] } else { 'api' }
            $workerKind = if ($Services.Count -gt 1) { $Services[1] } else { 'dispatch' }
            if ($target -notin @('api', 'workers') -or $Services.Count -gt 2) {
                throw 'debug target must be api or workers <kind>'
            }
            if ($workerKind -notin @('dispatch', 'notify', 'export', 'maintenance', 'ai', 'agent', 'backup')) {
                throw 'Unknown worker kind.'
            }
            Assert-DevDiskBudget
            Invoke-DevCompose -Arguments @('up', '-d', '--wait', '--wait-timeout', '600')
            Invoke-DevCompose -Arguments @('run', '--rm', '--no-deps', '--entrypoint', 'dlv', $target, 'version')
            # Compile once through the API toolchain without touching the live binary.
            Invoke-DevCompose -Arguments @('run', '--rm', '--no-deps', '--entrypoint', 'sh', 'api', '/workspace/deploy/development/build-backend.sh', '--debug')
            $running = @(& docker compose --env-file $devEnv -f $composeFile ps --services --status running)
            if ($LASTEXITCODE -ne 0) { throw 'Cannot inspect running services before debug.' }
            $restore = @('workers')
            if ($target -eq 'api') {
                $restore += 'api'
                $debugApiAddress = Get-DevPublishedAddress -Service 'api' -ContainerPort 8080
            }
            $restore = @($restore | Where-Object { $_ -in $running })
            try {
                Invoke-DevCompose -Arguments (@('stop') + $restore)
                Write-Host "Delve: 127.0.0.1:$DebugPort; source map /workspace -> $repoRoot"
                Write-Host 'The target starts when the debugger sends Continue. Exit to restore the normal services.'
                $arguments = @('run', '--rm', '--no-deps', '-p', "127.0.0.1:${DebugPort}:2345", '--entrypoint', 'dlv')
                if ($target -eq 'api') { $arguments += @('-p', "${debugApiAddress}:8080", '--use-aliases') }
                $kind = if ($target -eq 'api') { 'api' } else { "worker:$workerKind" }
                $arguments += @($target, 'exec', '/dev-bin/zongce.debug', '--headless', '--listen=:2345', '--api-version=2', '--accept-multiclient', '--', $kind)
                Invoke-DevCompose -Arguments $arguments
            } finally {
                if ($restore.Count -gt 0) {
                    Invoke-DevCompose -Arguments (@('up', '-d', '--wait', '--wait-timeout', '180') + $restore)
                }
            }
        }
        'clean' {
            Invoke-DevCompose -Arguments @('down', '--remove-orphans')
            docker volume rm easygpa-plus-dev_go-mod-cache easygpa-plus-dev_go-build-cache easygpa-plus-dev_frontend-node-modules easygpa-plus-dev_frontend-npm-cache easygpa-plus-dev_frontend-dist
            if ($LASTEXITCODE -ne 0) { throw 'Some cache volumes could not be removed.' }
            Write-Host 'Stopped containers and removed dependency/build caches. Database and object volumes were kept.'
        }
        'verify' {
            Assert-DevDiskBudget
            Invoke-DevCompose -Arguments @('config', '--quiet')
            Invoke-DevCompose -Arguments @('up', '-d', '--wait', '--wait-timeout', '600', '--remove-orphans')

            $frontendAddress = Get-DevPublishedAddress -Service 'frontend' -ContainerPort 5173
            $apiAddress = Get-DevPublishedAddress -Service 'api' -ContainerPort 8080
            $frontendResponse = Invoke-WebRequest -UseBasicParsing "http://$frontendAddress/" -TimeoutSec 10
            $apiResponse = Invoke-WebRequest -UseBasicParsing "http://$apiAddress/healthz" -TimeoutSec 10
            $unauthenticatedResponse = Invoke-WebRequest -UseBasicParsing -SkipHttpErrorCheck "http://$frontendAddress/api/v1/me" -TimeoutSec 10

            if ($frontendResponse.StatusCode -ne 200) {
                throw "Frontend smoke check returned HTTP $($frontendResponse.StatusCode)"
            }
            if ($apiResponse.StatusCode -ne 200) {
                throw "API health check returned HTTP $($apiResponse.StatusCode)"
            }
            if ($unauthenticatedResponse.StatusCode -ne 401) {
                throw "Vite-to-API proxy boundary returned HTTP $($unauthenticatedResponse.StatusCode), expected 401"
            }

            $workerProcesses = (& docker compose --env-file $devEnv -f $composeFile exec -T workers ps -o args) -join "`n"
            if ($LASTEXITCODE -ne 0) {
                throw 'Cannot inspect development worker processes.'
            }
            $workerCount = [regex]::Matches($workerProcesses, '/dev-bin/zongce worker:').Count
            if ($workerCount -ne 6) {
                throw "Expected 6 development workers, found $workerCount"
            }

            Invoke-DevCompose -Arguments @('run', '--rm', 'migrate')
            Invoke-DevTestSuite
            Invoke-DevStaticChecks

            Write-Host 'Development environment verification passed:'
            Write-Host "  frontend HTTP  $($frontendResponse.StatusCode)"
            Write-Host "  API health     $($apiResponse.StatusCode)"
            Write-Host "  unauth proxy   $($unauthenticatedResponse.StatusCode)"
            Write-Host "  workers        $workerCount"
            Write-Host '  migrations, tests, lint, vet, and builds passed'
        }
    }
} finally {
    Pop-Location
}
