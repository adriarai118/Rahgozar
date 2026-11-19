package backup

// Exclave (and other SagerNet-lineage) backups: a JSON object whose
// "profiles"/"groups" arrays hold base64 Android Parcels; each Parcel carries
// a Kryo 5 buffer (little-endian) written by the entity's serializeToBuffer.
// The readers below port Exclave's deserialize() methods, including their
// version branches, so older backups read too.

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"rahgozar/internal/model"
)

// KindExclave is an Exclave / SagerNet backup.
const KindExclave Kind = "exclave"

// ExGroup is a profile group of an Exclave backup.
type ExGroup struct {
	ID        int64
	Name      string
	Sub       bool   // subscription group
	URL       string // subscription link
	UserAgent string
}

// ExProfile is a server of an Exclave backup.
type ExProfile struct {
	Group   int64
	Profile *model.Profile
}

// ExclaveData is the decoded content of an Exclave backup.
type ExclaveData struct {
	Groups   []ExGroup
	Profiles []ExProfile
	Skipped  map[string]int // reason -> count (unsupported types, etc.)
}

func isExclave(b []byte) bool {
	t := bytes.TrimLeft(b[:min(len(b), 4096)], " \t\r\n\ufeff")
	return len(t) > 0 && t[0] == '{' && bytes.Contains(t, []byte(`"profiles"`)) && bytes.Contains(b[:min(len(b), 4096)], []byte(`"version"`))
}

// Exclave decodes an Exclave/SagerNet backup.
func Exclave(b []byte) (*ExclaveData, error) {
	var raw struct {
		Version  int      `json:"version"`
		Profiles []string `json:"profiles"`
		Groups   []string `json:"groups"`
	}
	if err := json.Unmarshal(b, &raw); err != nil || raw.Profiles == nil {
		return nil, ErrUnknown
	}
	d := &ExclaveData{Skipped: map[string]int{}}
	for _, s := range raw.Groups {
		buf, err := parcelBytes(s)
		if err != nil {
			continue
		}
		if g, err := readGroup(&kryo{b: buf}); err == nil {
			d.Groups = append(d.Groups, g)
		}
	}
	for _, s := range raw.Profiles {
		buf, err := parcelBytes(s)
		if err != nil {
			d.Skipped["فایل خراب"]++
			continue
		}
		gid, p, reason := readProxyEntity(buf)
		if p == nil {
			d.Skipped[reason]++
			continue
		}
		d.Profiles = append(d.Profiles, ExProfile{Group: gid, Profile: p})
	}
	if len(d.Profiles) == 0 && len(d.Groups) == 0 {
		return nil, errors.New("سروری در این پشتیبان پیدا نشد")
	}
	return d, nil
}

// parcelBytes undoes base64(Parcel.marshall()) of a single writeByteArray.
func parcelBytes(s string) ([]byte, error) {
	p, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	if len(p) < 4 {
		return nil, errSQLite
	}
	n := int(int32(binary.LittleEndian.Uint32(p)))
	if n < 0 || 4+n > len(p) {
		return nil, errors.New("bad parcel")
	}
	return p[4 : 4+n], nil
}

// ---------------- Kryo 5 reader ----------------

type kryo struct {
	b   []byte
	pos int
	err error
}

var errShort = errors.New("kryo: unexpected end")

func (k *kryo) take(n int) []byte {
	if k.err != nil || n < 0 || k.pos+n > len(k.b) {
		k.err = errShort
		return make([]byte, n)
	}
	v := k.b[k.pos : k.pos+n]
	k.pos += n
	return v
}

func (k *kryo) byte1() byte   { return k.take(1)[0] }
func (k *kryo) i32() int      { return int(int32(binary.LittleEndian.Uint32(k.take(4)))) }
func (k *kryo) i64() int64    { return int64(binary.LittleEndian.Uint64(k.take(8))) }
func (k *kryo) boolean() bool { return k.byte1() != 0 }
func (k *kryo) skip(n int)    { k.take(n) }
func (k *kryo) f64() float64  { return math.Float64frombits(binary.LittleEndian.Uint64(k.take(8))) }
func (k *kryo) left() int     { return len(k.b) - k.pos }
func (k *kryo) varint() int {
	v, shift := 0, 0
	for i := 0; i < 5; i++ {
		c := k.byte1()
		v |= int(c&0x7f) << shift
		if c&0x80 == 0 {
			return v
		}
		shift += 7
	}
	return v
}

// str reads a Kryo string; null reads as "".
func (k *kryo) str() string {
	b := k.byte1()
	if k.err != nil {
		return ""
	}
	if b&0x80 == 0 { // ASCII, last byte has bit 8 set
		start := k.pos - 1
		for {
			if k.pos > len(k.b) {
				k.err = errShort
				return ""
			}
			if k.b[k.pos-1]&0x80 != 0 {
				break
			}
			if k.pos == len(k.b) {
				k.err = errShort
				return ""
			}
			k.pos++
		}
		out := append([]byte(nil), k.b[start:k.pos]...)
		out[len(out)-1] &= 0x7f
		return string(out)
	}
	n := int(b & 0x3f)
	if b&0x40 != 0 {
		shift := 6
		for i := 0; i < 4; i++ {
			c := k.byte1()
			n |= int(c&0x7f) << shift
			shift += 7
			if c&0x80 == 0 {
				break
			}
		}
	}
	if n <= 1 {
		return "" // 0 = null, 1 = empty
	}
	units := make([]uint16, 0, n-1)
	for i := 0; i < n-1 && k.err == nil; i++ {
		c := k.byte1()
		switch c >> 4 {
		case 12, 13:
			units = append(units, uint16(c&0x1f)<<6|uint16(k.byte1()&0x3f))
		case 14:
			c2, c3 := k.byte1(), k.byte1()
			units = append(units, uint16(c&0x0f)<<12|uint16(c2&0x3f)<<6|uint16(c3&0x3f))
		default:
			units = append(units, uint16(c))
		}
	}
	return string(utf16.Decode(units))
}

// ---------------- entities ----------------

func readGroup(k *kryo) (ExGroup, error) {
	g := ExGroup{}
	ver := k.i32()
	g.ID = k.i64()
	k.i64() // userOrder
	k.boolean()
	g.Name = k.str()
	if k.i32() == 1 { // subscription
		g.Sub = true
		sv := k.i32()
		typ := k.i32()
		if sv < 7 && typ == 1 {
			return g, errors.New("OOCv1 subscription")
		}
		g.URL = k.str()
		if sv < 6 {
			k.boolean()
		}
		k.boolean() // deduplication
		if sv < 2 {
			k.boolean()
		}
		k.boolean() // updateWhenConnectedOnly
		g.UserAgent = k.str()
	}
	_ = ver
	return g, k.err
}

// Exclave entity types.
const (
	exSocks = 0
	exHTTP  = 1
	exSS    = 2
	exSSR   = 3
	exVMess = 4
	exVLESS = 5
	exTroj  = 6
	exSSH   = 17
	exWG    = 18
	exHy2   = 21
	exTUIC  = 23
	exAny   = 27
)

func readProxyEntity(b []byte) (group int64, p *model.Profile, reason string) {
	k := &kryo{b: b}
	k.i32() // version
	k.i64() // id
	group = k.i64()
	typ := k.i32()
	k.i64() // userOrder
	k.i64() // tx
	k.i64() // rx
	k.i32() // status
	k.i32() // ping
	k.str() // uuid
	k.str() // error
	n := k.varint()
	bean := k.take(n)
	if k.err != nil {
		return group, nil, "فایل خراب"
	}
	bk := &kryo{b: bean}
	var err error
	switch typ {
	case exSocks, exHTTP, exSS, exVMess, exVLESS, exTroj:
		p, err = readV2Ray(bk, typ)
	case exSSR:
		p, err = readSSR(bk)
	case exWG:
		p, err = readWG(bk)
	case exHy2:
		p, err = readHy2(bk)
	case exTUIC:
		p, err = readTUIC(bk)
	case exSSH:
		p, err = readSSH(bk)
	case exAny:
		p, err = readAnyTLS(bk)
	default:
		return group, nil, "نوع پشتیبانی‌نشده: " + exTypeName(typ)
	}
	if err == nil {
		p.Name, err = readExtra(bk)
	}
	if err == nil && bk.err == nil && bk.left() != 0 {
		err = fmt.Errorf("%d bytes left", bk.left())
	}
	if err == nil {
		err = bk.err
	}
	if err != nil {
		var u unsupported
		if errors.As(err, &u) {
			return group, nil, string(u)
		}
		return group, nil, "خطا در خواندن " + exTypeName(typ)
	}
	if p.Port <= 0 && p.Ports == "" {
		return group, nil, "پورت نامعتبر"
	}
	p.Flow = strings.TrimSuffix(p.Flow, "-udp443") // Exclave-only variant of xtls-rprx-vision
	if p.Name == "" {
		p.Name = p.Server
	}
	return group, p, ""
}

type unsupported string

func (u unsupported) Error() string { return string(u) }

func exTypeName(t int) string {
	switch t {
	case 8:
		return "زنجیره"
	case 9:
		return "Naive"
	case 13:
		return "کانفیگ دستی"
	case 14:
		return "Balancer"
	case 19:
		return "Mieru"
	case 25:
		return "Juicity"
	case 26:
		return "HTTP3"
	case 28:
		return "ShadowQUIC"
	case 29:
		return "TrustTunnel"
	case 30:
		return "Snell"
	case 31:
		return "MASQUE"
	}
	return "#" + strconv.Itoa(t)
}

// readExtra reads AbstractBean.deserializeFromBuffer's tail and returns the name.
func readExtra(k *kryo) (string, error) {
	ev := k.i32()
	name := k.str()
	et := k.i32()
	if et != 0 {
		k.str() // profileId
		if ev < 2 && et == 1 {
			k.str()
			if ev >= 1 {
				k.str()
			}
			for n := k.i32(); n > 0 && k.err == nil; n-- {
				k.str()
			}
		}
	}
	return strings.TrimSpace(name), k.err
}

func base(k *kryo, p *model.Profile) {
	p.Server = strings.TrimSpace(k.str())
	p.Port = k.i32()
}

func alpnList(s string) string {
	f := strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ',' || r == '\r' })
	for i := range f {
		f[i] = strings.TrimSpace(f[i])
	}
	return strings.Join(f, ",")
}

// readV2Ray ports StandardV2RayBean.deserialize and the subclasses built on it.
func readV2Ray(k *kryo, typ int) (*model.Profile, error) {
	p := &model.Profile{}
	switch typ {
	case exVMess:
		p.Type = model.TVMess
	case exVLESS:
		p.Type = model.TVLESS
	case exTroj:
		p.Type = model.TTrojan
	case exSS:
		p.Type = model.TShadowsocks
	case exSocks:
		p.Type = model.TSocks
	case exHTTP:
		p.Type = model.THTTP
	}
	// subclass prefix: version, and the legacy (pre-V2Ray-bean) layouts
	sub := -1
	legacy := false
	switch typ {
	case exTroj:
		sub = k.i32()
		legacy = sub < 2
	case exSS:
		sub = k.i32()
		legacy = sub < 5
	case exSocks:
		sub = k.i32()
		legacy = sub < 3
	case exHTTP:
		sub = k.i32()
		legacy = sub < 2
	}
	network, security := "tcp", ""
	if legacy {
		base(k, p)
		if typ == exTroj {
			p.Password = k.str()
			security = k.str()
			p.SNI = k.str()
			p.ALPN = alpnList(k.str())
			if sub == 1 {
				if security == "tls" {
					p.Insecure = k.boolean()
				} else {
					security = "tls"
					k.str()
				}
			}
		}
	} else {
		var err error
		network, security, err = readStd(k, p, typ)
		if err != nil {
			return nil, err
		}
	}
	switch typ {
	case exTroj:
		if !legacy {
			p.Password = k.str()
		}
	case exSS:
		p.Method = k.str()
		p.Password = k.str()
		if pl := k.str(); pl != "" {
			name, opts, _ := strings.Cut(pl, ";")
			p.Plugin, p.PluginOpts = name, opts
		}
		if sub >= 1 {
			k.boolean()
		}
		if sub == 2 || sub == 3 {
			k.boolean()
		}
		if sub == 3 {
			k.boolean()
		}
		if sub >= 6 {
			k.boolean()
		}
	case exSocks:
		if sub >= 1 {
			if k.i32() != 2 {
				return nil, unsupported("SOCKS4 پشتیبانی نمی‌شود")
			}
		}
		p.Username = k.str()
		p.Password = k.str()
		if sub <= 2 {
			if k.boolean() {
				security = "tls"
			}
			p.SNI = k.str()
		}
		if sub == 2 {
			p.Fingerprint = k.str()
		}
		if sub >= 4 {
			k.boolean()
		}
	case exHTTP:
		p.Username = k.str()
		p.Password = k.str()
		if sub <= 1 {
			if k.boolean() {
				security = "tls"
			}
			p.SNI = k.str()
			p.Fingerprint = k.str()
		}
		if sub >= 3 && sub <= 4 {
			k.boolean()
		}
		if sub >= 5 {
			if k.boolean() {
				return nil, unsupported("HTTP با CONNECT-UDP پشتیبانی نمی‌شود")
			}
			k.str()
		}
	}
	if k.err != nil {
		return nil, k.err
	}
	switch network {
	case "", "tcp":
		network = "tcp"
	case "splithttp":
		network = "xhttp"
	case "ws", "grpc", "httpupgrade", "kcp", "quic", "http":
	default:
		return nil, unsupported("ترنسپورت " + network + " پشتیبانی نمی‌شود")
	}
	if typ == exSS || typ == exSocks || typ == exHTTP {
		if network != "tcp" {
			return nil, unsupported("ترنسپورت " + network + " روی " + p.Type + " پشتیبانی نمی‌شود")
		}
		if typ == exSS {
			security = ""
		}
	} else {
		p.Network = network
	}
	switch security {
	case "", "none":
		p.Security = ""
	case "tls", "reality":
		p.Security = security
	default:
		return nil, unsupported("امنیت " + security + " پشتیبانی نمی‌شود")
	}
	if p.Type == model.TVMess && p.Method == "" {
		p.Method = "auto"
	}
	return p, nil
}

// readStd ports StandardV2RayBean.deserialize; returns transport and security.
func readStd(k *kryo, p *model.Profile, typ int) (string, string, error) {
	v := k.i32()
	base(k, p)
	uuid := k.str()
	enc := k.str()
	network := k.str()
	switch typ {
	case exVMess:
		p.UUID, p.Method = uuid, enc
	case exVLESS:
		p.UUID = uuid
		if enc != "" && enc != "none" {
			p.Method = enc // VLESS encryption
		}
	}
	fall := false
	switch network {
	case "tcp":
		p.HeaderType = k.str()
		p.Host = k.str()
		p.Path = k.str()
	case "kcp":
		p.HeaderType = k.str()
		p.Path = k.str() // mKCP seed
	case "ws":
		p.Host = k.str()
		p.Path = k.str()
		ed := k.i32()
		k.boolean()
		edh := ""
		if v >= 2 {
			edh = k.str()
		}
		if ed > 0 && (edh == "" || edh == "Sec-WebSocket-Protocol") && !strings.Contains(p.Path, "ed=") {
			sep := "?"
			if strings.Contains(p.Path, "?") {
				sep = "&"
			}
			p.Path += sep + "ed=" + strconv.Itoa(ed)
		}
	case "http":
		p.Host = k.str()
		p.Path = k.str()
	case "quic":
		p.HeaderType = k.str()
		k.str()
		k.str()
		fall = v < 16
	case "hysteria2":
		hyStd(k, v)
	case "splithttp":
		splitStd(k, v, p)
	case "mekya":
		if v >= 22 {
			k.str()
			k.str()
			k.str()
		}
	}
	// the Java switch falls through quic -> grpc -> meek -> httpupgrade -> hysteria2 for old versions
	if network == "grpc" || fall {
		s := k.str()
		if network == "grpc" {
			p.ServiceName = s
		}
		if v >= 8 && v <= 12 {
			k.str()
		}
		if v >= 33 {
			k.boolean()
			if k.boolean() && network == "grpc" {
				p.XMode = "multi"
			}
			if v < 34 {
				k.str()
			}
		}
		fall = v < 16
	}
	if network == "meek" || fall {
		if v >= 10 {
			k.str()
		}
		fall = v < 16
	}
	if network == "httpupgrade" || fall {
		if v >= 12 {
			h, pa := k.str(), k.str()
			if network == "httpupgrade" {
				p.Host, p.Path = h, pa
			}
		}
		if v >= 25 {
			k.i32()
			k.str()
		}
		fall = v < 16
	}
	if fall {
		hyStd(k, v)
	}

	security := k.str()
	switch security {
	case "tls":
		tlsStd(k, v, p)
	case "xtls":
		if v <= 8 {
			security = "tls"
			p.SNI = k.str()
			p.ALPN = alpnList(k.str())
			k.str()
		}
		if v < 16 { // Java falls through into the reality case
			realityStd(k, v, p)
		}
	case "reality":
		realityStd(k, v, p)
	}
	if typ == exVMess && v != 4 && v < 6 {
		p.AlterID = k.i32()
	}
	if typ == exVMess && v >= 4 {
		if v >= 17 {
			p.AlterID = k.i32()
		}
		k.boolean()
		k.boolean()
	}
	if typ == exVLESS && v >= 11 {
		p.Flow = k.str()
	}
	if v >= 7 && v <= 15 {
		k.i32()
	}
	if v >= 16 {
		k.str()
	}
	if v >= 19 {
		k.boolean()
		k.i32()
		k.str()
	}
	if v >= 32 {
		if typ == exSS || typ == exSocks {
			k.boolean()
		}
		k.boolean()
		k.str()
		k.i32()
		k.i32()
		k.i32()
		k.boolean()
	}
	if v >= 35 {
		k.boolean()
	}
	if v >= 36 {
		k.str()
	}
	if v >= 37 {
		k.str()
	}
	if v >= 38 {
		k.boolean()
	}
	if v >= 39 {
		k.str()
	}
	if v >= 41 {
		k.boolean()
	}
	if network == "hysteria2" || network == "meek" || network == "mekya" {
		return "", "", unsupported("ترنسپورت " + network + " پشتیبانی نمی‌شود")
	}
	return network, security, k.err
}

func hyStd(k *kryo, v int) {
	if v >= 14 {
		if v <= 28 {
			k.i32()
			k.i32()
		} else {
			k.i64()
			k.i64()
		}
		if v < 26 {
			k.str()
		}
	}
	if v >= 15 {
		k.str()
	}
}

func splitStd(k *kryo, v int, p *model.Profile) {
	if v >= 18 {
		p.Host = k.str()
		p.Path = k.str()
	}
	if v >= 20 {
		k.boolean()
	}
	if v >= 23 {
		p.XMode = k.str()
	}
	if v >= 24 {
		p.XExtra = k.str()
	}
}

func tlsStd(k *kryo, v int, p *model.Profile) {
	p.SNI = k.str()
	p.ALPN = alpnList(k.str())
	if v >= 1 {
		k.str() // certificates
		k.str()
	}
	if v >= 31 {
		k.str()
		k.str()
	}
	if v >= 3 {
		p.Insecure = k.boolean()
	}
	if v >= 9 {
		p.Fingerprint = k.str()
	}
	if v >= 21 {
		p.ECH = k.str()
		if v <= 28 {
			k.str()
		}
	}
	if v >= 31 {
		k.str()
		k.str()
	}
}

func realityStd(k *kryo, v int, p *model.Profile) {
	if v < 11 {
		return
	}
	p.SNI = k.str()
	p.PublicKey = k.str()
	p.ShortID = k.str()
	if v <= 25 {
		p.SpiderX = k.str()
	}
	p.Fingerprint = k.str()
	if v == 28 || v == 29 {
		k.str()
	}
	if v == 28 {
		k.str()
	}
	if v >= 27 {
		k.boolean()
	}
	if v >= 27 && v <= 29 {
		k.boolean()
	}
	if p.Fingerprint == "" {
		p.Fingerprint = "chrome"
	}
}

func readSSR(k *kryo) (*model.Profile, error) {
	p := &model.Profile{Type: model.TSSR}
	v := k.i32()
	base(k, p)
	p.Password = k.str()
	p.Method = k.str()
	p.Protocol = k.str()
	p.ProtocolParam = k.str()
	p.Obfs = k.str()
	p.ObfsParam = k.str()
	if v == 0 && p.Obfs == "tls_simple" {
		p.Obfs = "plain"
	}
	if v == 0 && p.Obfs == "tls1.2_ticket_fastauth" {
		p.Obfs = "tls1.2_ticket_auth"
	}
	return p, k.err
}

func readWG(k *kryo) (*model.Profile, error) {
	p := &model.Profile{Type: model.TWireGuard}
	v := k.i32()
	base(k, p)
	addr := k.str()
	p.PrivateKey = k.str()
	p.PeerPublicKey = k.str()
	p.PreSharedKey = k.str()
	if v <= 2 {
		if addr == "" {
			addr = "10.0.0.1/32\nfd59:7153:2388:b5fd:0000:0000:0000:0001/128"
		}
		fix := func(s string) string {
			if s == "" {
				return s
			}
			s = strings.NewReplacer("_", "/", "-", "+").Replace(s)
			for len(s) < 44 {
				s += "="
			}
			return s
		}
		p.PrivateKey, p.PeerPublicKey, p.PreSharedKey = fix(p.PrivateKey), fix(p.PeerPublicKey), fix(p.PreSharedKey)
	}
	for _, a := range strings.FieldsFunc(addr, func(r rune) bool { return r == '\n' || r == ',' }) {
		if a = strings.TrimSpace(a); a != "" {
			p.LocalAddress = append(p.LocalAddress, a)
		}
	}
	if v >= 1 {
		p.MTU = k.i32()
	}
	if v >= 2 {
		p.Reserved = parseReserved(k.str())
	}
	if v >= 4 {
		k.i32()
	}
	return p, k.err
}

func parseReserved(s string) []int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if strings.ContainsAny(s, ",") || len(s) <= 3 {
		var out []int
		for _, f := range strings.Split(s, ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(f)); err == nil && n >= 0 && n < 256 {
				out = append(out, n)
			}
		}
		if len(out) == 3 {
			return out
		}
		return nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 3 {
		return []int{int(b[0]), int(b[1]), int(b[2])}
	}
	return nil
}

func readHy2(k *kryo) (*model.Profile, error) {
	p := &model.Profile{Type: model.THysteria2}
	v := k.i32()
	base(k, p)
	p.Password = k.str()
	p.ObfsPassword = k.str()
	obfs := ""
	if p.ObfsPassword != "" && v < 9 {
		obfs = "salamander"
	}
	p.SNI = k.str()
	k.str()
	if v >= 4 {
		k.str()
		k.str()
	}
	k.str() // certificates
	p.Insecure = k.boolean()
	if v <= 2 {
		p.UpMbps, p.DownMbps = k.i32(), k.i32()
	} else {
		p.UpMbps, p.DownMbps = int(k.i64()), int(k.i64())
	}
	if v < 5 {
		k.boolean()
		k.i32()
		k.i32()
		k.i32()
		k.i32()
	}
	if v >= 2 {
		ports := strings.TrimSpace(k.str())
		var hop int64
		if v == 2 {
			hop = int64(k.i32())
		} else {
			hop = k.i64()
		}
		if strings.ContainsAny(ports, "-,:") {
			p.Ports = strings.ReplaceAll(ports, ":", "-")
			if hop > 0 {
				p.HopInterval = strconv.FormatInt(hop, 10) + "s"
			}
		} else if n, err := strconv.Atoi(ports); err == nil && n > 0 {
			p.Port = n
		}
	}
	if v >= 4 {
		k.str()
		k.str()
		k.str()
	}
	if v >= 6 {
		k.boolean()
	}
	if v >= 7 {
		k.i64()
		k.i64()
		p.Congestion = k.str()
		k.str()
	}
	if v >= 8 {
		k.boolean()
	}
	if v >= 9 {
		obfs = k.str()
		k.i32()
		k.i32()
		if obfs == "gecko" {
			return nil, unsupported("Hysteria2 با obfs gecko پشتیبانی نمی‌شود")
		}
		if obfs != "salamander" {
			p.ObfsPassword = ""
		}
	}
	if v >= 10 {
		k.str()
	}
	if v >= 11 {
		k.boolean()
	}
	if v >= 12 {
		k.str()
	}
	return p, k.err
}

func readTUIC(k *kryo) (*model.Profile, error) {
	p := &model.Profile{Type: model.TTUIC}
	v := k.i32()
	base(k, p)
	p.Password = k.str()
	k.str() // certificates
	p.UDPRelayMode = k.str()
	p.Congestion = k.str()
	p.ALPN = alpnList(k.str())
	k.boolean() // disableSNI
	k.boolean() // zeroRTT
	if v < 4 {
		k.i32()
	}
	p.SNI = k.str()
	p.UUID = k.str()
	if v >= 1 {
		p.Insecure = k.boolean()
	}
	if v >= 2 {
		k.str()
		k.str()
		k.str()
		k.str()
		k.str()
		k.str()
	}
	if v >= 3 {
		k.boolean()
	}
	if v >= 5 {
		k.boolean()
	}
	if v >= 6 {
		k.str()
	}
	if v >= 7 {
		k.str()
	}
	return p, k.err
}

func readSSH(k *kryo) (*model.Profile, error) {
	p := &model.Profile{Type: model.TSSH}
	v := k.i32()
	base(k, p)
	p.Username = k.str()
	switch k.i32() {
	case 1:
		p.Password = k.str()
	case 2:
		p.PrivateKey = k.str()
		p.Password = k.str() // passphrase
	}
	p.PublicKey = k.str()
	if v >= 1 {
		k.i32()
	}
	return p, k.err
}

func readAnyTLS(k *kryo) (*model.Profile, error) {
	p := &model.Profile{Type: model.TAnyTLS}
	v := k.i32()
	base(k, p)
	p.Password = k.str()
	if v >= 2 {
		k.i32()
		k.i32()
		k.i32()
	}
	sec := k.str()
	p.SNI = k.str()
	p.ALPN = alpnList(k.str())
	k.str()
	k.str()
	if v >= 4 {
		k.str()
		k.str()
	}
	p.Insecure = k.boolean()
	p.Fingerprint = k.str()
	p.ECH = k.str()
	if v <= 2 {
		k.str()
	}
	p.PublicKey = k.str()
	p.ShortID = k.str()
	rfp := k.str()
	if v >= 1 {
		k.boolean()
	}
	if v <= 2 {
		k.boolean()
	}
	if v >= 4 {
		k.str()
		k.str()
	}
	if v >= 5 {
		k.boolean()
	}
	if v >= 6 {
		k.str()
	}
	if v >= 7 {
		k.boolean()
	}
	if v >= 8 {
		k.str()
	}
	if sec == "reality" {
		p.Security = "reality"
		if rfp != "" {
			p.Fingerprint = rfp
		}
	} else {
		p.Security = "tls"
	}
	return p, k.err
}
