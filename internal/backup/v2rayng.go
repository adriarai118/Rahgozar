package backup

// v2rayNG backups: a zip of MMKV key-value files. PROFILE_FULL_CONFIG maps
// server ids to ProfileItem JSON, SUB maps subscription ids to their JSON,
// SERVER_RAW holds full configs of "custom" servers.

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"rahgozar/internal/model"
	"rahgozar/internal/parser"
)

// KindV2rayNG is a v2rayNG backup.
const KindV2rayNG Kind = "v2rayng"

func isV2rayNG(zr *zip.Reader) bool {
	has := map[string]bool{}
	for _, f := range zr.File {
		has[f.Name] = true
	}
	return has["PROFILE_FULL_CONFIG"] && (has["MAIN"] || has["SUB"])
}

// mmkv decodes an MMKV file: uint32 size, then (varint-length key, varint-
// length value) pairs, later pairs overriding earlier ones.
func mmkv(b []byte) (map[string][]byte, error) {
	if len(b) < 4 {
		return map[string][]byte{}, nil
	}
	n := int(binary.LittleEndian.Uint32(b))
	if n == 0 {
		return map[string][]byte{}, nil
	}
	if 4+n > len(b) {
		return nil, errors.New("mmkv: truncated")
	}
	body := b[4 : 4+n]
	parse := func(start int) (map[string][]byte, bool) {
		m := map[string][]byte{}
		i := start
		next := func() ([]byte, bool) {
			l, k := varint32(body[i:])
			if k == 0 || i+k+l > len(body) || l < 0 {
				return nil, false
			}
			v := body[i+k : i+k+l]
			i += k + l
			return v, true
		}
		for i < len(body) {
			key, ok := next()
			if !ok {
				return nil, false
			}
			val, ok := next()
			if !ok {
				return nil, false
			}
			if len(val) == 0 {
				delete(m, string(key))
			} else {
				m[string(key)] = val
			}
		}
		return m, true
	}
	// newer MMKV starts with an "item size holder" varint
	if _, k := varint32(body); k > 0 {
		if m, ok := parse(k); ok {
			return m, nil
		}
	}
	if m, ok := parse(0); ok {
		return m, nil
	}
	return nil, errors.New("mmkv: unreadable")
}

func varint32(b []byte) (int, int) {
	v, s := 0, 0
	for i := 0; i < len(b) && i < 5; i++ {
		v |= int(b[i]&0x7f) << s
		if b[i] < 0x80 {
			return v, i + 1
		}
		s += 7
	}
	return 0, 0
}

// mmkvString unwraps a string value (varint length + UTF-8).
func mmkvString(v []byte) string {
	l, k := varint32(v)
	if k == 0 || k+l > len(v) {
		return ""
	}
	return string(v[k : k+l])
}

type ngProfile struct {
	ConfigType     string `json:"configType"`
	SubscriptionID string `json:"subscriptionId"`
	Remarks        string `json:"remarks"`
	Server         string `json:"server"`
	ServerPort     string `json:"serverPort"`
	Password       string `json:"password"`
	Method         string `json:"method"`
	Flow           string `json:"flow"`
	Username       string `json:"username"`
	Network        string `json:"network"`
	HeaderType     string `json:"headerType"`
	Host           string `json:"host"`
	Path           string `json:"path"`
	Seed           string `json:"seed"`
	Mode           string `json:"mode"`
	ServiceName    string `json:"serviceName"`
	XhttpMode      string `json:"xhttpMode"`
	XhttpExtra     string `json:"xhttpExtra"`
	Security       string `json:"security"`
	SNI            string `json:"sni"`
	ALPN           string `json:"alpn"`
	FingerPrint    string `json:"fingerPrint"`
	Insecure       bool   `json:"insecure"`
	ECH            string `json:"echConfigList"`
	PublicKey      string `json:"publicKey"`
	ShortID        string `json:"shortId"`
	SpiderX        string `json:"spiderX"`
	SecretKey      string `json:"secretKey"`
	PreSharedKey   string `json:"preSharedKey"`
	LocalAddress   string `json:"localAddress"`
	Reserved       string `json:"reserved"`
	MTU            int    `json:"mtu"`
	Obfs           string `json:"obfs"`
	ObfsPassword   string `json:"obfsPassword"`
	PortHopping    string `json:"portHopping"`
	PortHopInt     string `json:"portHoppingInterval"`
	BandwidthDown  string `json:"bandwidthDown"`
	BandwidthUp    string `json:"bandwidthUp"`
}

// V2rayNG decodes a v2rayNG backup zip.
func V2rayNG(b []byte) (*ExclaveData, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil || !isV2rayNG(zr) {
		return nil, ErrUnknown
	}
	read := func(name string) map[string][]byte {
		for _, f := range zr.File {
			if f.Name == name {
				raw, err := readZipFile(f)
				if err != nil {
					return nil
				}
				m, _ := mmkv(raw)
				return m
			}
		}
		return nil
	}
	profiles, subs, raws := read("PROFILE_FULL_CONFIG"), read("SUB"), read("SERVER_RAW")
	d := &ExclaveData{Skipped: map[string]int{}}
	gid := map[string]int64{}
	ids := make([]string, 0, len(subs))
	for id := range subs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for i, id := range ids {
		var s struct {
			Remarks string `json:"remarks"`
			URL     string `json:"url"`
		}
		json.Unmarshal([]byte(mmkvString(subs[id])), &s)
		g := ExGroup{ID: int64(i + 1), Name: s.Remarks, URL: strings.TrimSpace(s.URL)}
		g.Sub = g.URL != ""
		gid[id] = g.ID
		d.Groups = append(d.Groups, g)
	}
	keys := make([]string, 0, len(profiles))
	for k := range profiles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var it ngProfile
		if json.Unmarshal([]byte(mmkvString(profiles[k])), &it) != nil {
			d.Skipped["فایل خراب"]++
			continue
		}
		g := gid[it.SubscriptionID]
		if it.ConfigType == "CUSTOM" {
			raw := mmkvString(raws[k])
			ps, _ := parser.ParseText(raw)
			if len(ps) == 0 {
				d.Skipped["کانفیگ دستی ناشناخته"]++
				continue
			}
			for i, p := range ps {
				p.Name = it.Remarks
				if len(ps) > 1 {
					p.Name += " #" + strconv.Itoa(i+1)
				}
				d.Profiles = append(d.Profiles, ExProfile{Group: g, Profile: p})
			}
			continue
		}
		p, err := ngToProfile(&it)
		if err != nil {
			d.Skipped[err.Error()]++
			continue
		}
		d.Profiles = append(d.Profiles, ExProfile{Group: g, Profile: p})
	}
	if len(d.Profiles) == 0 && len(d.Groups) == 0 {
		return nil, errors.New("سروری در این پشتیبان پیدا نشد")
	}
	return d, nil
}

func ngToProfile(it *ngProfile) (*model.Profile, error) {
	p := &model.Profile{Name: strings.TrimSpace(it.Remarks), Server: strings.TrimSpace(it.Server)}
	port := strings.TrimSpace(it.ServerPort)
	switch it.ConfigType {
	case "VMESS":
		p.Type, p.UUID, p.Method = model.TVMess, it.Password, it.Method
		if p.Method == "" {
			p.Method = "auto"
		}
	case "VLESS":
		p.Type, p.UUID, p.Flow = model.TVLESS, it.Password, it.Flow
		if it.Method != "" && it.Method != "none" {
			p.Method = it.Method
		}
	case "TROJAN":
		p.Type, p.Password, p.Flow = model.TTrojan, it.Password, ""
	case "SHADOWSOCKS":
		p.Type, p.Method, p.Password = model.TShadowsocks, it.Method, it.Password
	case "SOCKS":
		p.Type, p.Username, p.Password = model.TSocks, it.Username, it.Password
	case "HTTP":
		p.Type, p.Username, p.Password = model.THTTP, it.Username, it.Password
	case "WIREGUARD":
		p.Type, p.PrivateKey, p.PeerPublicKey, p.PreSharedKey, p.MTU = model.TWireGuard, it.SecretKey, it.PublicKey, it.PreSharedKey, it.MTU
		for _, a := range strings.Split(it.LocalAddress, ",") {
			if a = strings.TrimSpace(a); a != "" {
				p.LocalAddress = append(p.LocalAddress, a)
			}
		}
		p.Reserved = parseReserved(it.Reserved)
	case "HYSTERIA2":
		p.Type, p.Password, p.SNI, p.Insecure = model.THysteria2, it.Password, it.SNI, it.Insecure
		if it.ObfsPassword != "" {
			p.ObfsPassword = it.ObfsPassword
		}
		if h := strings.TrimSpace(it.PortHopping); h != "" {
			p.Ports = strings.ReplaceAll(h, ":", "-")
			if iv := strings.TrimSpace(it.PortHopInt); iv != "" {
				p.HopInterval = strings.TrimSuffix(iv, "s") + "s"
			}
		}
		p.UpMbps, p.DownMbps = mbps(it.BandwidthUp), mbps(it.BandwidthDown)
		if n := atoiSafe(port); n > 0 {
			p.Port = n
		} else if p.Ports == "" && strings.ContainsAny(port, "-,") {
			p.Ports = port
		}
		if p.Port == 0 && p.Ports == "" {
			return nil, errors.New("پورت نامعتبر")
		}
		return p, nil
	case "POLICYGROUP", "PROXYCHAIN", "CUSTOM_OUTBOUND":
		return nil, errors.New("نوع پشتیبانی‌نشده: " + strings.ToLower(it.ConfigType))
	default:
		return nil, errors.New("نوع پشتیبانی‌نشده: " + it.ConfigType)
	}
	p.Port = atoiSafe(port)
	if p.Port <= 0 || p.Port > 65535 {
		return nil, errors.New("پورت نامعتبر")
	}
	if p.Type == model.TWireGuard || p.Type == model.TShadowsocks || p.Type == model.TSocks || p.Type == model.THTTP {
		if p.Type == model.THTTP || p.Type == model.TSocks {
			if it.Security == "tls" {
				p.Security, p.SNI = "tls", it.SNI
			}
		}
		return p, nil
	}
	n := it.Network
	switch n {
	case "", "raw":
		n = "tcp"
	case "h2":
		n = "http"
	case "splithttp":
		n = "xhttp"
	}
	p.Network = n
	p.Host, p.Path, p.HeaderType = it.Host, it.Path, it.HeaderType
	switch n {
	case "kcp":
		p.Path = it.Seed
	case "grpc":
		p.ServiceName, p.Path = firstOf(it.ServiceName, it.Path), ""
		if it.Mode == "multi" {
			p.XMode = "multi"
		}
	case "xhttp":
		p.XMode, p.XExtra = firstOf(it.XhttpMode, it.Mode), it.XhttpExtra
	}
	if p.HeaderType == "none" {
		p.HeaderType = ""
	}
	switch it.Security {
	case "tls", "reality":
		p.Security = it.Security
	}
	p.SNI, p.Fingerprint, p.Insecure, p.ECH = it.SNI, it.FingerPrint, it.Insecure, it.ECH
	p.ALPN = alpnList(it.ALPN)
	if p.Security == "reality" {
		p.PublicKey, p.ShortID, p.SpiderX = it.PublicKey, it.ShortID, it.SpiderX
		if p.Fingerprint == "" {
			p.Fingerprint = "chrome"
		}
	}
	return p, nil
}

func atoiSafe(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

func mbps(s string) int {
	s = strings.TrimSpace(strings.ToLower(s))
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, _ := strconv.Atoi(s[:i])
	if strings.Contains(s, "g") {
		n *= 1000
	}
	return n
}

func firstOf(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}
