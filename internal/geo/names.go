package geo

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Persian names and common aliases used in server names.
var aliases = map[string]string{
	// Persian
	"آلمان": "de", "هلند": "nl", "فرانسه": "fr", "انگلیس": "gb", "انگلستان": "gb", "بریتانیا": "gb", "آمریکا": "us", "امریکا": "us",
	"کانادا": "ca", "ترکیه": "tr", "امارات": "ae", "دبی": "ae", "فنلاند": "fi", "سوئد": "se", "نروژ": "no", "دانمارک": "dk",
	"سوئیس": "ch", "اتریش": "at", "ایتالیا": "it", "اسپانیا": "es", "پرتغال": "pt", "لهستان": "pl", "رومانی": "ro", "بلغارستان": "bg",
	"مجارستان": "hu", "چک": "cz", "اوکراین": "ua", "روسیه": "ru", "ارمنستان": "am", "گرجستان": "ge", "آذربایجان": "az",
	"قزاقستان": "kz", "ژاپن": "jp", "کره": "kr", "سنگاپور": "sg", "هنگ‌کنگ": "hk", "هنگ کنگ": "hk", "هنگکنگ": "hk", "تایوان": "tw",
	"چین": "cn", "هند": "in", "استرالیا": "au", "برزیل": "br", "مکزیک": "mx", "ایران": "ir", "عراق": "iq", "عمان": "om",
	"قطر": "qa", "بحرین": "bh", "عربستان": "sa", "کویت": "kw", "اسرائیل": "il", "لتونی": "lv", "لیتوانی": "lt", "استونی": "ee",
	"ایرلند": "ie", "بلژیک": "be", "لوکزامبورگ": "lu", "یونان": "gr", "قبرس": "cy", "مولداوی": "md", "صربستان": "rs",
	"اسلواکی": "sk", "اسلوونی": "si", "کرواسی": "hr", "ایسلند": "is", "مالزی": "my", "اندونزی": "id", "تایلند": "th",
	"ویتنام": "vn", "فیلیپین": "ph", "پاکستان": "pk", "افغانستان": "af", "مصر": "eg", "آفریقای جنوبی": "za", "آرژانتین": "ar",
	"شیلی": "cl", "نیوزیلند": "nz", "اردن": "jo", "لبنان": "lb", "سوریه": "sy", "ترکمنستان": "tm", "ازبکستان": "uz",
	"تاجیکستان": "tj", "قرقیزستان": "kg", "بلاروس": "by", "آلبانی": "al", "مقدونیه": "mk", "بوسنی": "ba", "مونته‌نگرو": "me",
	// English aliases
	"usa": "us", "united states": "us", "america": "us", "uk": "gb", "england": "gb", "britain": "gb", "holland": "nl",
	"netherlands": "nl", "deutschland": "de", "germany": "de", "uae": "ae", "emirates": "ae", "dubai": "ae", "turkey": "tr",
	"türkiye": "tr", "turkiye": "tr", "korea": "kr", "south korea": "kr", "russia": "ru", "hongkong": "hk", "hong kong": "hk",
	"czech": "cz", "czechia": "cz", "iran": "ir", "frankfurt": "de", "amsterdam": "nl", "london": "gb", "paris": "fr",
	"helsinki": "fi", "stockholm": "se", "warsaw": "pl", "istanbul": "tr", "tokyo": "jp", "singapore": "sg", "toronto": "ca",
	"new york": "us", "los angeles": "us", "miami": "us", "dallas": "us", "seattle": "us", "chicago": "us", "virginia": "us",
	"nuremberg": "de", "falkenstein": "de", "moscow": "ru", "kyiv": "ua", "vienna": "at", "zurich": "ch", "milan": "it",
	"madrid": "es", "bucharest": "ro", "sofia": "bg", "riga": "lv", "tallinn": "ee", "vilnius": "lt", "yerevan": "am",
	"tbilisi": "ge", "baku": "az", "doha": "qa", "mumbai": "in", "sydney": "au", "seoul": "kr",
}

type namePat struct {
	key  string
	code string
}

var (
	pats     []namePat
	codeTok  = regexp.MustCompile(`(?:^|[^A-Za-z])([A-Z]{2})(?:[^A-Za-z]|$)`)
	knownCC  = map[string]bool{}
	notCodes = map[string]bool{"WS": true, "TCP": true, "IP": true, "OK": true, "VIP": true, "CDN": true, "GB": true, "MB": true, "TB": true, "TLS": true, "HY": true, "SS": true, "WG": true, "VL": true, "VM": true, "TR": false}
)

func init() {
	for cc, n := range englishNames {
		knownCC[strings.ToUpper(cc)] = true
		pats = append(pats, namePat{strings.ToLower(n), cc})
	}
	for k, v := range aliases {
		pats = append(pats, namePat{strings.ToLower(k), v})
	}
	// longest names first so "south africa" wins over "africa"-like substrings
	sort.Slice(pats, func(i, j int) bool { return len(pats[i].key) > len(pats[j].key) })
}

// flagEmoji extracts a regional-indicator flag (🇩🇪 → "de").
func flagEmoji(s string) string {
	rs := []rune(s)
	for i := 0; i+1 < len(rs); i++ {
		a, b := rs[i], rs[i+1]
		if a >= 0x1F1E6 && a <= 0x1F1FF && b >= 0x1F1E6 && b <= 0x1F1FF {
			return string([]rune{'a' + (a - 0x1F1E6), 'a' + (b - 0x1F1E6)})
		}
	}
	return ""
}

func isLetter(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// containsWord finds key in s with letter boundaries on both sides (a
// trailing Persian "ی" is allowed: «آلمانی»).
func containsWord(s, key string) bool {
	rs, ks := []rune(s), []rune(key)
	persian := false
	for _, r := range ks {
		if r >= 0x600 && r <= 0x6FF {
			persian = true
		}
	}
	for i := 0; i+len(ks) <= len(rs); i++ {
		match := true
		for j := range ks {
			if rs[i+j] != ks[j] {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if i > 0 && isLetter(rs[i-1]) {
			continue
		}
		e := i + len(ks)
		if e < len(rs) && isLetter(rs[e]) && !(persian && rs[e] == 'ی' && (e+1 == len(rs) || !isLetter(rs[e+1]))) {
			continue
		}
		return true
	}
	return false
}

// FromName guesses a country from a server name (flag emoji, Persian or
// English name, or a standalone ISO code such as "DE-01").
func FromName(name string) string {
	if cc := flagEmoji(name); cc != "" {
		if cc == "uk" {
			cc = "gb"
		}
		return cc
	}
	low := strings.ToLower(strings.ReplaceAll(name, "_", " "))
	for _, p := range pats {
		if containsWord(low, p.key) {
			return p.code
		}
	}
	for _, m := range codeTok.FindAllStringSubmatch(name, -1) {
		c := m[1]
		if knownCC[c] && !notCodes[c] {
			return strings.ToLower(c)
		}
	}
	return ""
}
