param(
    [string]$Root = 'C:\',
    [int64]$ThresholdMB = 500,
    [int]$Top = 200,
    [string]$OutDir = $PWD.Path
)

$ErrorActionPreference = 'SilentlyContinue'
$thresholdBytes = $ThresholdMB * 1MB
$folderSizes = New-Object 'System.Collections.Generic.Dictionary[string,int64]'
$largeFiles = New-Object 'System.Collections.Generic.List[object]'
$stack = New-Object 'System.Collections.Generic.Stack[string]'
$stack.Push((Resolve-Path -LiteralPath $Root).Path)
$scannedFiles = 0L
$scannedDirs = 0L
$start = Get-Date

function Add-FolderSize([string]$Path, [int64]$Bytes) {
    while ($Path) {
        if ($folderSizes.ContainsKey($Path)) {
            $folderSizes[$Path] += $Bytes
        } else {
            $folderSizes[$Path] = $Bytes
        }
        $parent = [System.IO.Directory]::GetParent($Path)
        if ($null -eq $parent) { break }
        $next = $parent.FullName
        if ($next -eq $Path) { break }
        $Path = $next
    }
}

Write-Host "Scanning $Root ... threshold=${ThresholdMB}MB"

while ($stack.Count -gt 0) {
    $dir = $stack.Pop()
    $scannedDirs++

    try {
        $attrs = [System.IO.File]::GetAttributes($dir)
        if (($attrs -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) { continue }
    } catch { continue }

    try {
        foreach ($file in [System.IO.Directory]::EnumerateFiles($dir)) {
            try {
                $info = [System.IO.FileInfo]::new($file)
                $size = [int64]$info.Length
                $scannedFiles++
                Add-FolderSize $info.DirectoryName $size
                if ($size -ge $thresholdBytes) {
                    $largeFiles.Add([pscustomobject]@{
                        GB = [math]::Round($size / 1GB, 2)
                        MB = [math]::Round($size / 1MB, 0)
                        Path = $info.FullName
                    })
                }
            } catch {}
        }
    } catch {}

    try {
        foreach ($subdir in [System.IO.Directory]::EnumerateDirectories($dir)) {
            try {
                $attrs = [System.IO.File]::GetAttributes($subdir)
                if (($attrs -band [System.IO.FileAttributes]::ReparsePoint) -eq 0) {
                    $stack.Push($subdir)
                }
            } catch {}
        }
    } catch {}

    if (($scannedDirs % 2000) -eq 0) {
        $elapsed = [math]::Max(((Get-Date) - $start).TotalSeconds, 1)
        Write-Host ("Dirs={0} Files={1} Speed={2}/s Current={3}" -f $scannedDirs, $scannedFiles, [math]::Round($scannedFiles / $elapsed, 0), $dir)
    }
}

$largeFolders = foreach ($entry in $folderSizes.GetEnumerator()) {
    if ($entry.Value -ge $thresholdBytes) {
        [pscustomobject]@{
            GB = [math]::Round($entry.Value / 1GB, 2)
            MB = [math]::Round($entry.Value / 1MB, 0)
            Path = $entry.Key
        }
    }
}

$largeFilesSorted = $largeFiles | Sort-Object GB -Descending | Select-Object -First $Top
$largeFoldersSorted = $largeFolders | Sort-Object GB -Descending | Select-Object -First $Top

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$filesCsv = Join-Path $OutDir "c-large-files-$stamp.csv"
$foldersCsv = Join-Path $OutDir "c-large-folders-$stamp.csv"
$largeFilesSorted | Export-Csv -LiteralPath $filesCsv -NoTypeInformation -Encoding UTF8
$largeFoldersSorted | Export-Csv -LiteralPath $foldersCsv -NoTypeInformation -Encoding UTF8

Write-Host ""
Write-Host "Large files >= ${ThresholdMB}MB: $filesCsv"
$largeFilesSorted | Format-Table -AutoSize
Write-Host ""
Write-Host "Large folders >= ${ThresholdMB}MB: $foldersCsv"
$largeFoldersSorted | Format-Table -AutoSize
Write-Host ""
Write-Host ("Done. Dirs={0}, Files={1}, Elapsed={2:n1}s" -f $scannedDirs, $scannedFiles, ((Get-Date) - $start).TotalSeconds)
