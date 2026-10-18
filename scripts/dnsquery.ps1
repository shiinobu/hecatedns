<#
.SYNOPSIS
  Sends one DNS query to a server on any port and prints the answer.

.DESCRIPTION
  Windows nslookup accepts -port but ignores it (checked on 2026-10-18), and
  Resolve-DnsName has no port option, so neither can test xdns on 127.0.0.1:5300.
  This script builds a plain UDP query by hand. It prints the response code, the
  answer records it understands (A, AAAA, CNAME) and the number of other records.

.EXAMPLE
  .\scripts\dnsquery.ps1 example.com
  .\scripts\dnsquery.ps1 ads.example.com -Type AAAA -Port 5300
  .\scripts\dnsquery.ps1 www.example.com -Type HTTPS -Server 192.168.1.99 -Port 53
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory, Position = 0)][string]$Name,
    [ValidateSet('A', 'AAAA', 'CNAME', 'TXT', 'MX', 'HTTPS', 'SVCB', 'NS', 'SOA')][string]$Type = 'A',
    [string]$Server = '127.0.0.1',
    [ValidateRange(1, 65535)][int]$Port = 5300,
    [ValidateRange(1, 60)][int]$TimeoutSeconds = 5
)

$typeCodes = @{ A = 1; NS = 2; CNAME = 5; SOA = 6; MX = 15; TXT = 16; AAAA = 28; SVCB = 64; HTTPS = 65 }
$rcodeNames = @('NOERROR', 'FORMERR', 'SERVFAIL', 'NXDOMAIN', 'NOTIMP', 'REFUSED')

function New-Query([string]$qname, [int]$qtype) {
    $id = Get-Random -Minimum 1 -Maximum 65535
    $bytes = [System.Collections.Generic.List[byte]]::new()
    $bytes.AddRange([byte[]]@([byte]($id -shr 8), [byte]($id -band 255), 1, 0, 0, 1, 0, 0, 0, 0, 0, 0))
    foreach ($label in $qname.TrimEnd('.').Split('.')) {
        if ($label.Length -lt 1 -or $label.Length -gt 63) { throw "label '$label' must be 1-63 characters" }
        $bytes.Add([byte]$label.Length)
        $bytes.AddRange([Text.Encoding]::ASCII.GetBytes($label))
    }
    $bytes.AddRange([byte[]]@(0, [byte]($qtype -shr 8), [byte]($qtype -band 255), 0, 1))
    , $bytes.ToArray()
}

# Returns the offset just after a (possibly compressed) name that starts at $pos.
function Skip-Name([byte[]]$msg, [int]$pos) {
    while ($true) {
        if ($pos -ge $msg.Length) { throw 'malformed reply: a name runs past the end of the message' }
        $len = $msg[$pos]
        if ($len -eq 0) { return $pos + 1 }
        if (($len -band 0xC0) -eq 0xC0) { return $pos + 2 }
        $pos += 1 + $len
    }
}

function Read-Name([byte[]]$msg, [int]$pos) {
    $labels = @()
    for ($hops = 0; $hops -lt 20; $hops++) {
        if ($pos -ge $msg.Length) { throw 'malformed reply: a name runs past the end of the message' }
        $len = $msg[$pos]
        if ($len -eq 0) { break }
        if (($len -band 0xC0) -eq 0xC0) { $pos = (($len -band 0x3F) -shl 8) + $msg[$pos + 1]; continue }
        $labels += [Text.Encoding]::ASCII.GetString($msg, $pos + 1, $len)
        $pos += 1 + $len
    }
    $labels -join '.'
}

$query = New-Query $Name $typeCodes[$Type]
$udp = [System.Net.Sockets.UdpClient]::new()
$udp.Client.ReceiveTimeout = $TimeoutSeconds * 1000
try {
    $udp.Connect($Server, $Port)
    [void]$udp.Send($query, $query.Length)
    $endpoint = [System.Net.IPEndPoint]::new([System.Net.IPAddress]::Any, 0)
    $reply = $udp.Receive([ref]$endpoint)
}
catch {
    Write-Error "no answer from ${Server}:$Port within $TimeoutSeconds s ($($_.Exception.Message))"
    exit 1
}
finally { $udp.Close() }

if ($reply.Length -lt 12) { Write-Error 'malformed reply: shorter than a DNS header'; exit 1 }
$rcode = $reply[3] -band 15
$rcodeText = if ($rcode -lt $rcodeNames.Count) { $rcodeNames[$rcode] } else { "RCODE$rcode" }
$answers = ($reply[6] -shl 8) + $reply[7]
$pos = Skip-Name $reply 12
$pos += 4
"{0} {1} -> {2}, {3} answer(s), {4} bytes" -f $Name, $Type, $rcodeText, $answers, $reply.Length
$other = 0
for ($i = 0; $i -lt $answers; $i++) {
    if ($pos -ge $reply.Length) { Write-Warning 'reply is cut short, remaining answers not shown'; break }
    $pos = Skip-Name $reply $pos
    if ($pos + 10 -gt $reply.Length) { Write-Warning 'reply is cut short, remaining answers not shown'; break }
    $rtype = ($reply[$pos] -shl 8) + $reply[$pos + 1]
    $ttl = ([uint32]$reply[$pos + 4] -shl 24) + ($reply[$pos + 5] -shl 16) + ($reply[$pos + 6] -shl 8) + $reply[$pos + 7]
    $rdlen = ($reply[$pos + 8] -shl 8) + $reply[$pos + 9]
    $rdata = $pos + 10
    switch ($rtype) {
        1 { "  A      {0}  ttl {1}" -f ([System.Net.IPAddress]::new($reply[$rdata..($rdata + 3)])), $ttl }
        28 { "  AAAA   {0}  ttl {1}" -f ([System.Net.IPAddress]::new($reply[$rdata..($rdata + 15)])), $ttl }
        5 { "  CNAME  {0}  ttl {1}" -f (Read-Name $reply $rdata), $ttl }
        default { $other++ }
    }
    $pos = $rdata + $rdlen
}
if ($other -gt 0) { "  ($other other record(s) not shown)" }
