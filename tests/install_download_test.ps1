$ErrorActionPreference = 'Stop'
$tokens = $null
$errors = $null
$ast = [System.Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot '../install.ps1'), [ref]$tokens, [ref]$errors)
if ($errors.Count) { throw $errors[0] }
$function = $ast.Find({ param($node) $node -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Invoke-ReleaseDownload' }, $true)
. ([scriptblock]::Create($function.Extent.Text))
function Say($message) {}
function Start-Sleep($Seconds) { $script:delays += $Seconds }
function Invoke-WebRequest {
    param($Uri, $OutFile, $UseBasicParsing, $TimeoutSec, $Headers)
    $script:calls++
    if ($Uri -ne 'https://example.test/asset' -or $OutFile -ne 'test.zip' -or -not $UseBasicParsing -or $TimeoutSec -ne 60 -or $Headers.Accept -ne 'application/octet-stream') { throw 'Download options changed' }
    if ($script:calls -le $script:failures) {
        $error = [Exception]::new('Download failure')
        $error | Add-Member -NotePropertyName Response -NotePropertyValue ([pscustomobject]@{StatusCode=$script:status})
        throw $error
    }
    return [pscustomobject]@{Content='verified later by installer'}
}

$script:calls=0; $script:delays=@(); $script:failures=2; $script:status=504
$result=Invoke-ReleaseDownload -Uri 'https://example.test/asset' -OutFile 'test.zip'
if ($script:calls -ne 3 -or ($script:delays -join ',') -ne '2,4' -or $result.Content -ne 'verified later by installer') { throw 'Transient download did not recover with bounded backoff' }

foreach ($status in @(403,504)) {
    $script:calls=0; $script:delays=@(); $script:failures=10; $script:status=$status
    $failed=$false
    try { Invoke-ReleaseDownload -Uri 'https://example.test/asset' -OutFile 'test.zip' | Out-Null } catch { $failed=$true }
    $expected=if ($status -eq 403) { 1 } else { 3 }
    if (-not $failed -or $script:calls -ne $expected) { throw "Incorrect retry limit for HTTP $status" }
}
Write-Host 'Installer download retry checks passed.'
