# SPDX-License-Identifier: Apache-2.0
# Starts only loopback processes and synthetic data owned by this acceptance run.
[CmdletBinding()]
param(
    [ValidateSet('start', 'stop', 'status', 'seed-client', 'verify', 'conformance', 'worker')]
    [string]$Action = 'status',
    [ValidateSet('gateway', 'upstream', 'mock', 'conformance')]
    [string]$Role = 'gateway',
    [string]$ExpectedRunId = '',
    [string]$ModelId = 'provider-onboarding-chat',
    [switch]$SkipBuild
)

$ErrorActionPreference = 'Stop'
$nmgRepositoryRoot = [IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$nmgDataRoot = [IO.Path]::GetFullPath((Join-Path $nmgRepositoryRoot '.dev/provider-onboarding'))
$nmgConfigPath = Join-Path $nmgDataRoot 'synthetic.config.json'
$nmgControlPath = Join-Path $nmgDataRoot 'control.json'
$nmgClientPath = Join-Path $nmgDataRoot 'desktop-client.synthetic.json'
$nmgJournalPath = Join-Path $nmgDataRoot 'client-seed-journal.json'
$nmgPorts = @{ gateway = 18891; upstream = 18892; mock = 18893 }

function Assert-OwnedDirectory {
    foreach ($nmgPath in @((Join-Path $nmgRepositoryRoot '.dev'), $nmgDataRoot, (Join-Path $nmgDataRoot 'bin'))) {
        if (Test-Path -LiteralPath $nmgPath) {
            $nmgItem = Get-Item -LiteralPath $nmgPath -Force
            if (-not $nmgItem.PSIsContainer -or ($nmgItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
                throw 'The acceptance directory must be an ordinary directory in this checkout.'
            }
        }
    }
    if (Test-Path -LiteralPath $nmgDataRoot) {
        foreach ($nmgItem in @(Get-ChildItem -LiteralPath $nmgDataRoot -Force)) {
            if ($nmgItem.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Acceptance files must remain in the owned directory; links are not followed.' }
        }
    }
}
function Write-LocalJSON($nmgPath, $nmgValue) {
    $nmgTemporary = "$nmgPath.tmp"
    $nmgValue | ConvertTo-Json -Depth 30 | Set-Content -LiteralPath $nmgTemporary -Encoding utf8
    Move-Item -LiteralPath $nmgTemporary -Destination $nmgPath -Force
}
function Read-SyntheticConfig {
    if (-not (Test-Path -LiteralPath $nmgConfigPath)) { throw 'Run start first; the synthetic configuration is absent.' }
    $nmgLocalConfig = Get-Content -LiteralPath $nmgConfigPath -Raw | ConvertFrom-Json
    if (-not $nmgLocalConfig.synthetic_only -or $nmgLocalConfig.gateway_url -ne 'http://127.0.0.1:18891' -or $nmgLocalConfig.upstream_url -ne 'http://127.0.0.1:18892/v1' -or $nmgLocalConfig.admin_email -ne 'provider-onboarding@example.test' -or $nmgLocalConfig.upstream_key -ne 'synthetic-provider-onboarding-upstream') {
        throw 'Acceptance configuration does not match the owned synthetic fixture.'
    }
    return $nmgLocalConfig
}
function Get-Listener($nmgPort) {
    return @(Get-NetTCPConnection -State Listen -LocalPort $nmgPort -ErrorAction SilentlyContinue)
}
function Get-OwnedProcess($nmgRecord, [switch]$Worker) {
    $nmgProcessId = if ($Worker) { $nmgRecord.worker_pid } else { $nmgRecord.listener_pid }
    $nmgStarted = if ($Worker) { $nmgRecord.worker_started_utc } else { $nmgRecord.listener_started_utc }
    $nmgExecutable = if ($Worker) { $nmgRecord.worker_path } else { $nmgRecord.binary_path }
    if (-not $nmgProcessId) { return $null }
    $nmgProcess = Get-Process -Id $nmgProcessId -ErrorAction SilentlyContinue
    if (-not $nmgProcess) { return $null }
    if ($nmgProcess.Path -ne $nmgExecutable -or $nmgProcess.StartTime.ToUniversalTime().Ticks -ne ([DateTime]$nmgStarted).ToUniversalTime().Ticks) {
        throw 'Recorded PID now belongs to a different process; no process was stopped.'
    }
    return $nmgProcess
}
function Read-WorkerChild($nmgRecord, $nmgRunId) {
    $nmgWorkerStatePath = Join-Path $nmgDataRoot "$($nmgRecord.role).worker-state.json"
    if (-not (Test-Path -LiteralPath $nmgWorkerStatePath)) { return }
    $nmgWorkerState = Get-Content -LiteralPath $nmgWorkerStatePath -Raw | ConvertFrom-Json
    if ($nmgWorkerState.run_id -ne $nmgRunId -or $nmgWorkerState.binary_path -ne $nmgRecord.binary_path) { return }
    $nmgRecord.listener_pid = $nmgWorkerState.process_id
    $nmgRecord.listener_started_utc = $nmgWorkerState.started_utc
}
function Invoke-SyntheticAPI($nmgPath, $nmgMethod = 'GET', $nmgBody = $null, $nmgToken = '') {
    $nmgHeaders = @{}
    if ($nmgToken) { $nmgHeaders.Authorization = "Bearer $nmgToken" }
    $nmgArguments = @{ Uri = "http://127.0.0.1:18891/api/console/v1$nmgPath"; Method = $nmgMethod; Headers = $nmgHeaders; TimeoutSec = 20 }
    if ($null -ne $nmgBody) {
        $nmgArguments.ContentType = 'application/json'
        $nmgArguments.Body = $nmgBody | ConvertTo-Json -Depth 30 -Compress
    }
    try { return Invoke-RestMethod @nmgArguments }
    catch { throw "Synthetic console operation failed: $nmgMethod $nmgPath. Inspect the local runtime evidence without replaying an uncertain mutation." }
}
function Get-SyntheticSession($nmgConfig) {
    return Invoke-SyntheticAPI '/login' 'POST' @{ email = $nmgConfig.admin_email; password = $nmgConfig.admin_password }
}
function Assert-RunningOwnership {
    if (-not (Test-Path -LiteralPath $nmgControlPath)) { throw 'The acceptance control file is absent.' }
    $nmgControl = Get-Content -LiteralPath $nmgControlPath -Raw | ConvertFrom-Json
    foreach ($nmgRecord in $nmgControl.processes) {
        $nmgProcess = Get-OwnedProcess $nmgRecord
        $nmgListeners = Get-Listener $nmgRecord.port
        if (-not $nmgProcess -or $nmgListeners.Count -eq 0 -or @($nmgListeners | Where-Object OwningProcess -ne $nmgProcess.Id).Count) {
            throw 'Acceptance process/listener ownership is not intact.'
        }
    }
}

Assert-OwnedDirectory
if ($Action -eq 'worker') {
    $nmgConfig = Read-SyntheticConfig
    if (-not $ExpectedRunId -or $nmgConfig.run_id -ne $ExpectedRunId) { throw 'Acceptance worker run identity mismatch.' }
    $nmgBinary = [IO.Path]::GetFullPath((Join-Path $nmgDataRoot "bin/$Role.exe"))
    $nmgBinaryArguments = @()
    $nmgChildEnvironment = @{}
    switch ($Role) {
        'gateway' {
            $nmgChildEnvironment.NMG_LISTEN = '127.0.0.1:18891'
            $nmgChildEnvironment.NMG_DB_DRIVER = 'sqlite'
            $nmgChildEnvironment.NMG_DB_DSN = Join-Path $nmgDataRoot 'gateway.sqlite'
            $nmgChildEnvironment.NMG_MASTER_KEY = $nmgConfig.master_key
            $nmgChildEnvironment.NMG_ADMIN_EMAIL = $nmgConfig.admin_email
            $nmgChildEnvironment.NMG_ADMIN_PASSWORD = $nmgConfig.admin_password
        }
        'upstream' { $nmgChildEnvironment.NMG_FIXTURE_LISTEN = '127.0.0.1:18892'; $nmgChildEnvironment.NMG_FIXTURE_API_KEY = $nmgConfig.upstream_key }
        'mock' { $nmgChildEnvironment.NMG_MOCK_API_KEY = $nmgConfig.mock_key; $nmgBinaryArguments = @('--listen', '127.0.0.1:18893') }
        'conformance' {
            $nmgChildEnvironment.NMG_BASE_URL = 'http://127.0.0.1:18893'
            $nmgChildEnvironment.NMG_API_KEY = $nmgConfig.mock_key
            $nmgChildEnvironment.NMG_FIXTURE_FILE = Join-Path $nmgRepositoryRoot 'testdata/conformance.json'
        }
    }
    Set-Location -LiteralPath $nmgRepositoryRoot
    $nmgStartInfo = [Diagnostics.ProcessStartInfo]::new()
    $nmgStartInfo.FileName = $nmgBinary
    $nmgStartInfo.WorkingDirectory = $nmgRepositoryRoot
    $nmgStartInfo.UseShellExecute = $false
    $nmgStartInfo.CreateNoWindow = $true
    $nmgStartInfo.RedirectStandardOutput = $true
    $nmgStartInfo.RedirectStandardError = $true
    # Build a fresh child environment. Only these ordinary Windows runtime
    # variables are read; no existing NMG variables, keys or proxy values are read.
    $nmgStartInfo.Environment.Clear()
    foreach ($nmgName in @('SystemRoot', 'WINDIR', 'TEMP', 'TMP')) {
        $nmgSystemValue = [Environment]::GetEnvironmentVariable($nmgName, 'Process')
        if ($nmgSystemValue) { $nmgStartInfo.Environment[$nmgName] = $nmgSystemValue }
    }
    foreach ($nmgName in $nmgChildEnvironment.Keys) { $nmgStartInfo.Environment[$nmgName] = $nmgChildEnvironment[$nmgName] }
    foreach ($nmgArgument in $nmgBinaryArguments) { $nmgStartInfo.ArgumentList.Add($nmgArgument) }
    $nmgChild = [Diagnostics.Process]::Start($nmgStartInfo)
    $nmgOutputCopy = $nmgChild.StandardOutput.BaseStream.CopyToAsync([Console]::OpenStandardOutput())
    $nmgErrorCopy = $nmgChild.StandardError.BaseStream.CopyToAsync([Console]::OpenStandardError())
    if ($Role -ne 'conformance') {
        Write-LocalJSON (Join-Path $nmgDataRoot "$Role.worker-state.json") @{ run_id = $ExpectedRunId; process_id = $nmgChild.Id; started_utc = $nmgChild.StartTime.ToUniversalTime().ToString('o'); binary_path = $nmgBinary }
    }
    $nmgChild.WaitForExit()
    [void]$nmgOutputCopy.GetAwaiter().GetResult()
    [void]$nmgErrorCopy.GetAwaiter().GetResult()
    exit $nmgChild.ExitCode
}

if ($Action -eq 'start') {
    foreach ($nmgPort in $nmgPorts.Values) {
        if ((Get-Listener $nmgPort).Count) { throw "Acceptance port $nmgPort is occupied. Its existing owner was not changed." }
    }
    if (Test-Path -LiteralPath $nmgControlPath) {
        $nmgOldControl = Get-Content -LiteralPath $nmgControlPath -Raw | ConvertFrom-Json
        foreach ($nmgRecord in $nmgOldControl.processes) {
            if ((Get-OwnedProcess $nmgRecord) -or (Get-OwnedProcess $nmgRecord -Worker)) { throw 'An acceptance process is still alive. Run stop before restarting.' }
        }
    }
    [void](New-Item -ItemType Directory -Path (Join-Path $nmgDataRoot 'bin') -Force)
    $nmgRunId = [Guid]::NewGuid().ToString()
    if (Test-Path -LiteralPath $nmgConfigPath) {
        $nmgConfig = Read-SyntheticConfig
        $nmgConfig.run_id = $nmgRunId
    } else {
        $nmgMasterBytes = [Security.Cryptography.RandomNumberGenerator]::GetBytes(32)
        $nmgConfig = [pscustomobject]@{ synthetic_only = $true; run_id = $nmgRunId; gateway_url = 'http://127.0.0.1:18891'; upstream_url = 'http://127.0.0.1:18892/v1'; public_model_id = $ModelId; upstream_model_id = 'synthetic-chat-v1'; admin_email = 'provider-onboarding@example.test'; admin_password = 'SyntheticOnboardingOnly123!'; upstream_key = 'synthetic-provider-onboarding-upstream'; mock_key = 'nmg_mock_provider_onboarding'; master_key = [Convert]::ToBase64String($nmgMasterBytes) }
    }
    Write-LocalJSON $nmgConfigPath $nmgConfig
    if (-not $SkipBuild) {
        $nmgGoVersion = & go version
        if ($LASTEXITCODE -ne 0 -or $nmgGoVersion -notmatch '\bgo1\.27\.1\b') { throw 'Use the Go 1.27.1 version pinned in go.mod.' }
        foreach ($nmgBuild in @(@('gateway', './cmd/model-gateway'), @('upstream', './cmd/provider-onboarding-fixture'), @('mock', './cmd/mock-gateway'), @('conformance', './cmd/conformance'))) {
            & go -C $nmgRepositoryRoot build -o (Join-Path $nmgDataRoot "bin/$($nmgBuild[0]).exe") $nmgBuild[1]
            if ($LASTEXITCODE -ne 0) { throw "Acceptance $($nmgBuild[0]) build failed." }
        }
    }
    $nmgControl = [pscustomobject]@{ run_id = $nmgRunId; repository = $nmgRepositoryRoot; data_directory = $nmgDataRoot; started_utc = [DateTime]::UtcNow.ToString('o'); stopped_utc = $null; processes = @() }
    Write-LocalJSON $nmgControlPath $nmgControl
    $nmgPowerShell = (Get-Command pwsh -ErrorAction Stop).Source
    foreach ($nmgRole in @('upstream', 'mock', 'gateway')) {
        $nmgBinaryPath = [IO.Path]::GetFullPath((Join-Path $nmgDataRoot "bin/$nmgRole.exe"))
        if (-not (Test-Path -LiteralPath $nmgBinaryPath)) { throw "Acceptance $nmgRole binary is absent." }
        $nmgArguments = @('-NoProfile', '-File', "`"$PSCommandPath`"", '-Action', 'worker', '-Role', $nmgRole, '-ExpectedRunId', $nmgRunId)
        $nmgWorker = Start-Process -FilePath $nmgPowerShell -ArgumentList $nmgArguments -WorkingDirectory $nmgRepositoryRoot -WindowStyle Hidden -RedirectStandardOutput (Join-Path $nmgDataRoot "$nmgRole.stdout.log") -RedirectStandardError (Join-Path $nmgDataRoot "$nmgRole.stderr.log") -PassThru
        $nmgRecord = [pscustomobject]@{ role = $nmgRole; port = $nmgPorts[$nmgRole]; worker_pid = $nmgWorker.Id; worker_started_utc = $nmgWorker.StartTime.ToUniversalTime().ToString('o'); worker_path = $nmgPowerShell; binary_path = $nmgBinaryPath; listener_pid = $null; listener_started_utc = $null }
        $nmgControl.processes += $nmgRecord
        Write-LocalJSON $nmgControlPath $nmgControl
        $nmgDeadline = [DateTime]::UtcNow.AddSeconds(25)
        do {
            Start-Sleep -Milliseconds 200
            $nmgListeners = Get-Listener $nmgRecord.port
            if ($nmgWorker.HasExited) { throw "Acceptance $nmgRole worker exited. Its local logs are retained." }
        } until ($nmgListeners.Count -or [DateTime]::UtcNow -ge $nmgDeadline)
        Read-WorkerChild $nmgRecord $nmgRunId
        Write-LocalJSON $nmgControlPath $nmgControl
        if (-not $nmgListeners.Count) { throw "Acceptance $nmgRole listener did not start. Run stop to clean up the recorded workers." }
        $nmgListenerProcess = Get-Process -Id $nmgListeners[0].OwningProcess -ErrorAction Stop
        if ($nmgListenerProcess.Path -ne $nmgBinaryPath) { throw 'Acceptance listener was claimed by another executable; that process was not stopped.' }
        $nmgRecord.listener_pid = $nmgListenerProcess.Id
        $nmgRecord.listener_started_utc = $nmgListenerProcess.StartTime.ToUniversalTime().ToString('o')
        Write-LocalJSON $nmgControlPath $nmgControl
    }
    Assert-RunningOwnership
    [void](Invoke-RestMethod 'http://127.0.0.1:18891/healthz' -TimeoutSec 10)
    Write-Host 'Synthetic acceptance ready: Gateway http://127.0.0.1:18891; upstream http://127.0.0.1:18892/v1; Mock http://127.0.0.1:18893.'
    Write-Host "Public model to create in the console: $ModelId; upstream model: synthetic-chat-v1."
    Write-Host "Synthetic configuration: $nmgConfigPath"
    return
}
if ($Action -eq 'stop') {
    if (-not (Test-Path -LiteralPath $nmgControlPath)) { Write-Host 'No acceptance control file; no process was stopped.'; return }
    $nmgControl = Get-Content -LiteralPath $nmgControlPath -Raw | ConvertFrom-Json
    # Validate every recorded process first so a mismatched PID cannot lead to
    # partial cleanup of another run. Never kill by port, image name or pattern.
    $nmgOwned = @()
    foreach ($nmgRecord in $nmgControl.processes) {
        if (-not $nmgRecord.listener_pid) { Read-WorkerChild $nmgRecord $nmgControl.run_id }
        foreach ($nmgIsWorker in @($false, $true)) {
            $nmgProcess = Get-OwnedProcess $nmgRecord -Worker:$nmgIsWorker
            if ($nmgProcess) { $nmgOwned += $nmgProcess }
        }
    }
    foreach ($nmgProcess in $nmgOwned) { Stop-Process -Id $nmgProcess.Id -ErrorAction SilentlyContinue }
    Start-Sleep -Milliseconds 500
    $nmgControl.stopped_utc = [DateTime]::UtcNow.ToString('o')
    Write-LocalJSON $nmgControlPath $nmgControl
    foreach ($nmgPort in $nmgPorts.Values) {
        if ((Get-Listener $nmgPort).Count) { throw "Port $nmgPort has a surviving listener. No unrecorded process was stopped." }
    }
    Write-Host 'Only recorded acceptance processes were stopped; synthetic data and evidence were retained.'
    return
}
if ($Action -eq 'status') {
    foreach ($nmgRole in @('gateway', 'upstream', 'mock')) {
        $nmgListeners = Get-Listener $nmgPorts[$nmgRole]
        Write-Host "$nmgRole port=$($nmgPorts[$nmgRole]) listeners=$($nmgListeners.Count)"
    }
    if (Test-Path -LiteralPath $nmgControlPath) {
        $nmgStatusControl = Get-Content -LiteralPath $nmgControlPath -Raw | ConvertFrom-Json
        if ($nmgStatusControl.stopped_utc) { Write-Host 'Acceptance run is stopped; its data and evidence are retained.' }
        else { Assert-RunningOwnership; Write-Host 'Recorded process and listener ownership verified.' }
    }
    return
}

Assert-RunningOwnership
$nmgConfig = Read-SyntheticConfig
if ($Action -eq 'conformance') {
    & $PSCommandPath -Action worker -Role conformance -ExpectedRunId $nmgConfig.run_id
    if ($LASTEXITCODE -ne 0) { throw 'Mock HTTP conformance failed.' }
    return
}
$nmgSession = Get-SyntheticSession $nmgConfig
if ($Action -eq 'seed-client') {
    if (Test-Path -LiteralPath $nmgClientPath) { Write-Host "Existing synthetic Desktop client configuration retained: $nmgClientPath"; return }
    if (Test-Path -LiteralPath $nmgJournalPath) { throw 'A prior client seed has an uncertain result. Inspect its journal and console before retrying; mutations were not replayed.' }
    $nmgModelResponse = Invoke-SyntheticAPI '/admin/models' 'GET' $null $nmgSession.session_token
    if (-not @($nmgModelResponse.items | Where-Object { $_.id -eq $ModelId -and $_.enabled }).Count) { throw 'Publish the selected model through the console before creating the synthetic client.' }
    $nmgJournal = @{ phase = 'credit_pending'; synthetic_only = $true; started_utc = [DateTime]::UtcNow.ToString('o') }
    Write-LocalJSON $nmgJournalPath $nmgJournal
    [void](Invoke-SyntheticAPI "/admin/users/$($nmgSession.user.id)/credit" 'POST' @{ amount = 100000; currency = 'USD'; reason = 'Synthetic isolated provider onboarding acceptance' } $nmgSession.session_token)
    $nmgJournal.phase = 'key_pending'
    Write-LocalJSON $nmgJournalPath $nmgJournal
    $nmgCreated = Invoke-SyntheticAPI '/keys' 'POST' @{ name = 'Synthetic Desktop onboarding acceptance'; model_ids = @($ModelId); allowed_ips = @('127.0.0.1'); quota_limit = 50000; requests_per_minute = 60; concurrent_requests = 2 } $nmgSession.session_token
    Write-LocalJSON $nmgClientPath @{ synthetic_only = $true; gateway_url = $nmgConfig.gateway_url; model_id = $ModelId; api_key = $nmgCreated.api_key; key_id = $nmgCreated.key.id; evidence_scope = 'Local synthetic relay and billing only; no real provider acceptance' }
    $nmgJournal.phase = 'complete'
    Write-LocalJSON $nmgJournalPath $nmgJournal
    Write-Host "Synthetic Desktop client configuration created: $nmgClientPath"
    return
}
if ($Action -eq 'verify') {
    if (-not (Test-Path -LiteralPath $nmgClientPath)) { throw 'Run seed-client after console publication first.' }
    $nmgClient = Get-Content -LiteralPath $nmgClientPath -Raw | ConvertFrom-Json
    $nmgChannelsBefore = Invoke-SyntheticAPI '/admin/channels' 'GET' $null $nmgSession.session_token
    $nmgModelsBefore = Invoke-SyntheticAPI '/admin/models' 'GET' $null $nmgSession.session_token
    $nmgSavedChannel = @($nmgChannelsBefore.items | Where-Object { $_.base_url -eq $nmgConfig.upstream_url -and $_.model_ids.PSObject.Properties[$nmgClient.model_id].Value -eq 'synthetic-chat-v1' })
    if ($nmgSavedChannel.Count -ne 1) { throw 'Expected one wizard-created synthetic channel with the public-to-upstream mapping.' }
    $nmgSavedDiscovery = Invoke-SyntheticAPI "/admin/channels/$($nmgSavedChannel[0].id)/discover" 'POST' @{} $nmgSession.session_token
    if (-not $nmgSavedDiscovery.complete -or $nmgSavedDiscovery.models.Count -ne 2) { throw 'Saved synthetic channel discovery did not return a complete deduplicated directory.' }
    $nmgPagedDiscovery = Invoke-SyntheticAPI '/admin/channels/discover-preview' 'POST' @{ kind = 'anthropic'; base_url = 'http://127.0.0.1:18892/paginated'; api_key = $nmgConfig.upstream_key } $nmgSession.session_token
    if (-not $nmgPagedDiscovery.complete -or $nmgPagedDiscovery.pages -ne 2 -or $nmgPagedDiscovery.models.Count -ne 2) { throw 'Anthropic discovery pagination or cross-page duplicate handling failed.' }
    $nmgPartialDiscovery = Invoke-SyntheticAPI '/admin/channels/discover-preview' 'POST' @{ kind = 'anthropic'; base_url = 'http://127.0.0.1:18892/partial'; api_key = $nmgConfig.upstream_key } $nmgSession.session_token
    if ($nmgPartialDiscovery.complete -or $nmgPartialDiscovery.pages -ne 1 -or $nmgPartialDiscovery.models.Count -ne 1 -or $nmgPartialDiscovery.warnings.Count -eq 0) { throw 'A later-page discovery failure did not preserve an explicitly partial list.' }
    $nmgFailedDiscovery = Invoke-WebRequest "$($nmgConfig.gateway_url)/api/console/v1/admin/channels/discover-preview" -Method POST -Headers @{ Authorization = "Bearer $($nmgSession.session_token)" } -ContentType 'application/json' -Body (@{ kind = 'openai'; base_url = 'http://127.0.0.1:18892/failed'; api_key = $nmgConfig.upstream_key } | ConvertTo-Json -Compress) -TimeoutSec 20 -SkipHttpErrorCheck
    if ($nmgFailedDiscovery.StatusCode -ne 502 -or $nmgFailedDiscovery.Content -match [regex]::Escape($nmgConfig.upstream_key)) { throw 'First-page discovery failure did not return a sanitized failure.' }
    $nmgChannelsAfter = Invoke-SyntheticAPI '/admin/channels' 'GET' $null $nmgSession.session_token
    $nmgModelsAfter = Invoke-SyntheticAPI '/admin/models' 'GET' $null $nmgSession.session_token
    if (($nmgChannelsBefore | ConvertTo-Json -Depth 30 -Compress) -ne ($nmgChannelsAfter | ConvertTo-Json -Depth 30 -Compress) -or ($nmgModelsBefore | ConvertTo-Json -Depth 30 -Compress) -ne ($nmgModelsAfter | ConvertTo-Json -Depth 30 -Compress)) { throw 'Discovery changed channels, mappings, catalog, access or pricing.' }
    Write-LocalJSON (Join-Path $nmgDataRoot 'discovery-evidence.json') @{ synthetic_only = $true; checked_utc = [DateTime]::UtcNow.ToString('o'); saved_channel = $nmgSavedDiscovery; paginated_preview = $nmgPagedDiscovery; later_page_failure = $nmgPartialDiscovery; first_page_failure_status = $nmgFailedDiscovery.StatusCode; mapping_catalog_pricing_unchanged = $true }
    $nmgHeaders = @{ Authorization = "Bearer $($nmgClient.api_key)" }
    $nmgChatBody = @{ model = $nmgClient.model_id; max_tokens = 32; messages = @(@{ role = 'user'; content = 'Synthetic local acceptance.' }); fixture_request_unknown = @{ retained = $true } }
    $nmgResponse = Invoke-WebRequest "$($nmgConfig.gateway_url)/v1/chat/completions" -Method POST -Headers $nmgHeaders -ContentType 'application/json' -Body ($nmgChatBody | ConvertTo-Json -Depth 10 -Compress) -TimeoutSec 20
    $nmgNative = $nmgResponse.Content | ConvertFrom-Json
    if (-not $nmgNative.fixture_response_unknown.retained -or $nmgNative.usage.prompt_tokens_details.cached_tokens -ne 4 -or $nmgNative.usage.completion_tokens_details.reasoning_tokens -ne 2 -or $nmgNative.usage.fixture_usage_unknown -ne 3) { throw 'Native response unknown/cache/reasoning fields changed.' }
    $nmgChatBody.stream = $true
    $nmgStream = Invoke-WebRequest "$($nmgConfig.gateway_url)/v1/chat/completions" -Method POST -Headers $nmgHeaders -ContentType 'application/json' -Body ($nmgChatBody | ConvertTo-Json -Depth 10 -Compress) -TimeoutSec 20
    if ($nmgStream.Content -notmatch 'fixture_event_unknown' -or $nmgStream.Content -notmatch 'cached_tokens' -or $nmgStream.Content -notmatch '\[DONE\]') { throw 'Native SSE framing or unknown/cache fields changed.' }
    $nmgHolds = Invoke-SyntheticAPI '/admin/reservations' 'GET' $null $nmgSession.session_token
    $nmgRequestIds = @([string]$nmgResponse.Headers['x-request-id'][0], [string]$nmgStream.Headers['x-request-id'][0])
    $nmgEvidenceHolds = @($nmgHolds.items | Where-Object { $_.request_id -in $nmgRequestIds })
    if ($nmgEvidenceHolds.Count -ne 2 -or @($nmgEvidenceHolds | Where-Object { $_.state -ne 'settled' -or $_.actual_tokens -ne 17 -or -not $_.pricing_json -or $_.usage_json -notmatch 'fixture_usage_unknown' }).Count) { throw 'Native usage did not settle exactly once with retained price and usage snapshots.' }
    $nmgLedger = Invoke-SyntheticAPI '/admin/ledger' 'GET' $null $nmgSession.session_token
    foreach ($nmgRequestId in $nmgRequestIds) {
        if (@($nmgLedger.items | Where-Object { $_.request_id -eq $nmgRequestId -and $_.kind -eq 'usage' }).Count -ne 1) { throw 'Expected one settlement ledger entry per request.' }
    }
    $nmgReconciliationIds = @()
    foreach ($nmgUsageMode in @('partial', 'missing')) {
        $nmgChatBody.stream = $false
        $nmgChatBody.fixture_usage_mode = $nmgUsageMode
        $nmgIncomplete = Invoke-WebRequest "$($nmgConfig.gateway_url)/v1/chat/completions" -Method POST -Headers $nmgHeaders -ContentType 'application/json' -Body ($nmgChatBody | ConvertTo-Json -Depth 10 -Compress) -TimeoutSec 20
        $nmgReconciliationIds += [string]$nmgIncomplete.Headers['x-request-id'][0]
    }
    $nmgChatBody.Remove('fixture_usage_mode')
    $nmgChatBody.fixture_error_mode = 'rate-limited'
    $nmgNativeError = Invoke-WebRequest "$($nmgConfig.gateway_url)/v1/chat/completions" -Method POST -Headers $nmgHeaders -ContentType 'application/json' -Body ($nmgChatBody | ConvertTo-Json -Depth 10 -Compress) -TimeoutSec 20 -SkipHttpErrorCheck
    if ($nmgNativeError.StatusCode -ne 429 -or $nmgNativeError.Content -notmatch 'fixture_error_unknown') { throw 'Native error status or unknown fields changed.' }
    $nmgHolds = Invoke-SyntheticAPI '/admin/reservations' 'GET' $null $nmgSession.session_token
    $nmgReconciliationHolds = @($nmgHolds.items | Where-Object { $_.request_id -in $nmgReconciliationIds })
    if ($nmgReconciliationHolds.Count -ne 2 -or @($nmgReconciliationHolds | Where-Object { $_.state -ne 'reconciliation' -or -not $_.upstream_accepted -or ($_.usage_json | ConvertFrom-Json).complete -or $_.usage_json -match 'Synthetic local provider acceptance succeeded' }).Count) { throw 'Partial or missing accepted upstream usage was not retained for reconciliation.' }
    $nmgErrorRequestId = [string]$nmgNativeError.Headers['x-request-id'][0]
    $nmgErrorHolds = @($nmgHolds.items | Where-Object { $_.request_id -eq $nmgErrorRequestId })
    if ($nmgErrorHolds.Count -ne 1 -or $nmgErrorHolds[0].state -ne 'released') { throw 'Native failed request did not release its hold.' }
    $nmgUpstreamEvidence = Invoke-RestMethod 'http://127.0.0.1:18892/fixture/evidence' -Headers @{ Authorization = "Bearer $($nmgConfig.upstream_key)" } -TimeoutSec 10
    if ($nmgUpstreamEvidence.unknown_request_fields_received -lt 2) { throw 'Unknown native request fields were not received by the upstream.' }
    $nmgEvidencePath = Join-Path $nmgDataRoot 'native-billing-evidence.json'
    Write-LocalJSON $nmgEvidencePath @{ synthetic_only = $true; checked_utc = [DateTime]::UtcNow.ToString('o'); model_id = $nmgClient.model_id; assertions = @('unknown native request field retained', 'unknown native response field retained', 'cache and reasoning usage retained', 'SSE framing/unknown events retained', 'immutable pricing snapshot retained', '17 native tokens settled', 'one settlement ledger entry per request', 'accepted partial/missing usage retained for reconciliation', 'generated content omitted from durable usage', 'native 429 and unknown error fields retained', 'native error hold released'); upstream_counts = $nmgUpstreamEvidence; settled_reservations = $nmgEvidenceHolds; reconciliation_reservations = $nmgReconciliationHolds; native_error_reservations = $nmgErrorHolds }
    Write-Host "Synthetic native relay and billing acceptance passed. Evidence: $nmgEvidencePath"
}
