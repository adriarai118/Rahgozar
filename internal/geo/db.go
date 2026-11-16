// Package geo maps servers to countries (for flags): from the server name
// first, then by looking up the server IP in an embedded IPv4 database
// (ip-location-db geo-whois-asn-country, CC0).
package geo

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
)

//go:embed ipv4.bin.gz
var ipv4gz []byte

var (
	once   sync.Once
	starts []uint32
	ccIdx  []byte
	codes  []string
)

func load() {
	r, err := gzip.NewReader(bytes.NewReader(ipv4gz))
	if err != nil {
		return
	}
	b, err := io.ReadAll(r)
	if err != nil || len(b) < 8 {
		return
	}
	n := int(binary.LittleEndian.Uint32(b))
	off := 4
	codes = make([]string, n+1)
	for i := 1; i <= n; i++ {
		codes[i] = strings.ToLower(string(b[off : off+2]))
		off += 2
	}
	m := int(binary.LittleEndian.Uint32(b[off:]))
	off += 4
	starts = make([]uint32, m)
	ccIdx = make([]byte, m)
	for i := 0; i < m && off+5 <= len(b); i++ {
		starts[i] = binary.LittleEndian.Uint32(b[off:])
		ccIdx[i] = b[off+4]
		off += 5
	}
}

// LookupIP returns the lower-case country code of an IPv4 address ("" if unknown).
func LookupIP(ip net.IP) string {
	once.Do(load)
	v4 := ip.To4()
	if v4 == nil || len(starts) == 0 {
		return ""
	}
	x := binary.BigEndian.Uint32(v4)
	i := sort.Search(len(starts), func(i int) bool { return starts[i] > x }) - 1
	if i < 0 {
		return ""
	}
	return codes[ccIdx[i]]
}
