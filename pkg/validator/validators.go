package validator

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

type ValidatorFunc func(args ...interface{}) (interface{}, error)

var builtins = map[string]ValidatorFunc{
	"notEmptyString":    NotEmptyString,
	"isEmail":           IsEmail,
	"isRFC5321":         IsRFC5321,
	"isRFC5322":         IsRFC5322,
	"isURL":             IsURL,
	"isHTTP":            IsHTTP,
	"isHTTPS":           IsHTTPS,
	"isE164":            IsE164,
	"isValidPhone":      IsValidPhone,
	"getPhoneCountry":   GetPhoneCountry,
	"isValidUUID":       IsValidUUID,
	"getUUIDVersion":    GetUUIDVersion,
	"isValidCoordinate": IsValidCoordinate,
	"isWithinBounds":    IsWithinBounds,
	"getDistance":       GetDistance,
	"isHexColor":        IsHexColor,
	"isRGBColor":        IsRGBColor,
	"isHSLColor":        IsHSLColor,
	"isISOCurrency":     IsISOCurrency,
	"isValidLocale":     IsValidLocale,
	"getBCP47":          GetBCP47,
	"isValidIBAN":       IsValidIBAN,
	"getIBANCountry":    GetIBANCountry,
	"getIBANChecksum":   GetIBANChecksum,
	"isIPv4":            IsIPv4,
	"isIPv6":            IsIPv6,
	"isPrivateIP":       IsPrivateIP,
	"getIPVersion":      GetIPVersion,

	"notBlank":         NotEmptyString,
	"isBlank":          IsBlank,
	"notNil":           NotNil,
	"isNil":            IsNil,
	"between":          Between,
	"nonNegative":      NonNegative,
	"isPositive":       IsPositive,
	"hasPrefix":        HasPrefix,
	"hasSuffix":        HasSuffix,
	"contains":         Contains,
	"equalsIgnoreCase": EqualsIgnoreCase,
	"minLen":           MinLen,
	"maxLen":           MaxLen,
	"startsWithAny":    StartsWithAny,
	"oneOf":            OneOf,

	"requireAdminKey": RequireAdminKey,
	"nobody":          Nobody,

	"log": nil,
}

func BuiltinRegistered(name string) bool {
	_, ok := builtins[name]
	return ok
}

func GetBuiltin(name string) (ValidatorFunc, bool) {
	fn, ok := builtins[name]
	return fn, ok
}

func RequireAdminKey(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("requireAdminKey requires 1 argument (the presented key)")
	}
	want := strings.TrimSpace(os.Getenv("FOOKEE_ADMIN_KEY"))
	if want == "" {
		return false, nil
	}
	got, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	return strings.TrimSpace(got) == want, nil
}

func Nobody(args ...interface{}) (interface{}, error) {
	_ = args
	return false, nil
}

func NotEmptyString(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("notEmptyString requires 1 argument")
	}
	s, ok := args[0].(string)
	if !ok || s == "" {
		return false, nil
	}
	return strings.TrimSpace(s) != "", nil
}

func IsBlank(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isBlank requires 1 argument")
	}
	if args[0] == nil {
		return true, nil
	}
	s, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	return strings.TrimSpace(s) == "", nil
}

func NotNil(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("notNil requires 1 argument")
	}
	return args[0] != nil, nil
}

func IsNil(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isNil requires 1 argument")
	}
	return args[0] == nil, nil
}

func Between(args ...interface{}) (interface{}, error) {
	if len(args) < 3 {
		return false, fmt.Errorf("between requires 3 arguments (n, min, max)")
	}
	v, ok1 := toFloat(args[0])
	lo, ok2 := toFloat(args[1])
	hi, ok3 := toFloat(args[2])
	if !ok1 || !ok2 || !ok3 {
		return false, nil
	}
	return v >= lo && v <= hi, nil
}

func NonNegative(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("nonNegative requires 1 argument")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return false, nil
	}
	return v >= 0, nil
}

func IsPositive(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isPositive requires 1 argument")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return false, nil
	}
	return v > 0, nil
}

func HasPrefix(args ...interface{}) (interface{}, error) {
	if len(args) < 2 {
		return false, fmt.Errorf("hasPrefix requires 2 arguments (s, prefix)")
	}
	s, ok1 := args[0].(string)
	p, ok2 := args[1].(string)
	if !ok1 || !ok2 {
		return false, nil
	}
	return strings.HasPrefix(s, p), nil
}

func HasSuffix(args ...interface{}) (interface{}, error) {
	if len(args) < 2 {
		return false, fmt.Errorf("hasSuffix requires 2 arguments (s, suffix)")
	}
	s, ok1 := args[0].(string)
	suf, ok2 := args[1].(string)
	if !ok1 || !ok2 {
		return false, nil
	}
	return strings.HasSuffix(s, suf), nil
}

func Contains(args ...interface{}) (interface{}, error) {
	if len(args) < 2 {
		return false, fmt.Errorf("contains requires 2 arguments (s, substr)")
	}
	s, ok1 := args[0].(string)
	sub, ok2 := args[1].(string)
	if !ok1 || !ok2 {
		return false, nil
	}
	return strings.Contains(s, sub), nil
}

func EqualsIgnoreCase(args ...interface{}) (interface{}, error) {
	if len(args) < 2 {
		return false, fmt.Errorf("equalsIgnoreCase requires 2 arguments (a, b)")
	}
	a, ok1 := args[0].(string)
	b, ok2 := args[1].(string)
	if !ok1 || !ok2 {
		return false, nil
	}
	return strings.EqualFold(a, b), nil
}

func MinLen(args ...interface{}) (interface{}, error) {
	if len(args) < 2 {
		return false, fmt.Errorf("minLen requires 2 arguments (s, n)")
	}
	s, ok1 := args[0].(string)
	nf, ok2 := toFloat(args[1])
	if !ok1 || !ok2 {
		return false, nil
	}
	n := int(nf)
	if n < 0 {
		return false, nil
	}
	return utf8.RuneCountInString(s) >= n, nil
}

func MaxLen(args ...interface{}) (interface{}, error) {
	if len(args) < 2 {
		return false, fmt.Errorf("maxLen requires 2 arguments (s, n)")
	}
	s, ok1 := args[0].(string)
	nf, ok2 := toFloat(args[1])
	if !ok1 || !ok2 {
		return false, nil
	}
	n := int(nf)
	if n < 0 {
		return false, nil
	}
	return utf8.RuneCountInString(s) <= n, nil
}

func StartsWithAny(args ...interface{}) (interface{}, error) {
	if len(args) < 2 {
		return false, fmt.Errorf("startsWithAny requires at least 2 arguments (s, prefix, ...)")
	}
	s, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	for _, a := range args[1:] {
		p, ok := a.(string)
		if ok && strings.HasPrefix(s, p) {
			return true, nil
		}
	}
	return false, nil
}

func OneOf(args ...interface{}) (interface{}, error) {
	if len(args) < 2 {
		return false, fmt.Errorf("oneOf requires at least 2 arguments (value, option, ...)")
	}
	v := args[0]
	for _, opt := range args[1:] {
		if v == opt {
			return true, nil
		}
	}
	return false, nil
}

func IsEmail(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isEmail requires 1 argument")
	}
	email, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	re := regexp.MustCompile(`^[a-zA-Z0-9._+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	return re.MatchString(email), nil
}

func IsRFC5321(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isRFC5321 requires 1 argument")
	}
	email, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	re := regexp.MustCompile(`^.+@[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)
	return re.MatchString(email), nil
}

func IsRFC5322(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isRFC5322 requires 1 argument")
	}
	email, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	re := regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	return re.MatchString(email), nil
}

func IsURL(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isURL requires 1 argument")
	}
	urlStr, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	u, err := url.Parse(urlStr)
	if err != nil {
		return false, nil
	}
	return u.Scheme != "" && u.Host != "", nil
}

func IsHTTP(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isHTTP requires 1 argument")
	}
	urlStr, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	u, err := url.Parse(urlStr)
	if err != nil {
		return false, nil
	}
	return u.Scheme == "http", nil
}

func IsHTTPS(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isHTTPS requires 1 argument")
	}
	urlStr, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	u, err := url.Parse(urlStr)
	if err != nil {
		return false, nil
	}
	return u.Scheme == "https", nil
}

func IsE164(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isE164 requires 1 argument")
	}
	phone, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	re := regexp.MustCompile(`^\+[1-9]\d{1,14}$`)
	return re.MatchString(phone), nil
}

func IsValidPhone(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isValidPhone requires 1 argument")
	}
	phone, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	cleaned := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || r == '+' {
			return r
		}
		return -1
	}, phone)
	return len(cleaned) >= 10 && len(cleaned) <= 15, nil
}

func GetPhoneCountry(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("getPhoneCountry requires 1 argument")
	}
	phone, ok := args[0].(string)
	if !ok {
		return "", nil
	}
	countryMap := map[string]string{
		"+1":  "US",
		"+44": "GB",
		"+33": "FR",
		"+49": "DE",
		"+39": "IT",
		"+34": "ES",
		"+31": "NL",
		"+32": "BE",
		"+41": "CH",
		"+43": "AT",
		"+90": "TR",
	}
	for prefix, country := range countryMap {
		if strings.HasPrefix(phone, prefix) {
			return country, nil
		}
	}
	return "", nil
}

func IsValidUUID(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isValidUUID requires 1 argument")
	}
	uuidStr, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	return re.MatchString(strings.ToLower(uuidStr)), nil
}

func GetUUIDVersion(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return 0, fmt.Errorf("getUUIDVersion requires 1 argument")
	}
	uuidStr, ok := args[0].(string)
	if !ok {
		return 0, nil
	}
	parts := strings.Split(uuidStr, "-")
	if len(parts) < 3 {
		return 0, nil
	}
	versionChar := parts[2][0]
	version := int(versionChar - '0')
	if version < 1 || version > 5 {
		return 0, nil
	}
	return float64(version), nil
}

func IsValidCoordinate(args ...interface{}) (interface{}, error) {
	if len(args) < 2 {
		return false, fmt.Errorf("isValidCoordinate requires 2 arguments (lat, lng)")
	}
	lat, ok1 := toFloat(args[0])
	lng, ok2 := toFloat(args[1])
	if !ok1 || !ok2 {
		return false, nil
	}
	return lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180, nil
}

func IsWithinBounds(args ...interface{}) (interface{}, error) {
	if len(args) < 5 {
		return false, fmt.Errorf("isWithinBounds requires 5 arguments (lat, lng, centerLat, centerLng, radiusKm)")
	}
	lat, ok1 := toFloat(args[0])
	lng, ok2 := toFloat(args[1])
	centerLat, ok3 := toFloat(args[2])
	centerLng, ok4 := toFloat(args[3])
	radiusKm, ok5 := toFloat(args[4])
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		return false, nil
	}
	dist := getHaversineDistance(lat, lng, centerLat, centerLng)
	return dist <= radiusKm, nil
}

func GetDistance(args ...interface{}) (interface{}, error) {
	if len(args) < 4 {
		return 0, fmt.Errorf("getDistance requires 4 arguments (lat, lng, centerLat, centerLng)")
	}
	lat, ok1 := toFloat(args[0])
	lng, ok2 := toFloat(args[1])
	centerLat, ok3 := toFloat(args[2])
	centerLng, ok4 := toFloat(args[3])
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return 0, nil
	}
	return getHaversineDistance(lat, lng, centerLat, centerLng), nil
}

func getHaversineDistance(lat1, lng1, lat2, lng2 float64) float64 {
	const R = 6371
	dLat := toRad(lat2 - lat1)
	dLng := toRad(lng2 - lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLng/2)*math.Sin(dLng/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}

func toRad(deg float64) float64 {
	return deg * math.Pi / 180
}

func IsHexColor(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isHexColor requires 1 argument")
	}
	color, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	re := regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	return re.MatchString(color), nil
}

func IsRGBColor(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isRGBColor requires 1 argument")
	}
	color, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	re := regexp.MustCompile(`^rgb\((\d{1,3}),\s*(\d{1,3}),\s*(\d{1,3})\)$`)
	return re.MatchString(color), nil
}

func IsHSLColor(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isHSLColor requires 1 argument")
	}
	color, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	re := regexp.MustCompile(`^hsl\((\d{1,3}),\s*(\d{1,3})%,\s*(\d{1,3})%\)$`)
	return re.MatchString(color), nil
}

func IsISOCurrency(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isISOCurrency requires 1 argument")
	}
	code, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	re := regexp.MustCompile(`^[A-Z]{3}$`)
	return re.MatchString(code), nil
}

func IsValidLocale(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isValidLocale requires 1 argument")
	}
	locale, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	re := regexp.MustCompile(`^[a-z]{2}(-[A-Z]{2})?$`)
	return re.MatchString(locale), nil
}

func GetBCP47(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("getBCP47 requires 1 argument")
	}
	locale, ok := args[0].(string)
	if !ok {
		return "", nil
	}
	if strings.Contains(locale, "_") {
		return strings.ReplaceAll(locale, "_", "-"), nil
	}
	return locale, nil
}

func IsValidIBAN(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isValidIBAN requires 1 argument")
	}
	iban, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	iban = strings.ReplaceAll(iban, " ", "")
	re := regexp.MustCompile(`^[A-Z]{2}[0-9]{2}[A-Z0-9]{1,30}$`)
	return re.MatchString(iban), nil
}

func GetIBANCountry(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("getIBANCountry requires 1 argument")
	}
	iban, ok := args[0].(string)
	if !ok {
		return "", nil
	}
	iban = strings.ReplaceAll(iban, " ", "")
	if len(iban) < 2 {
		return "", nil
	}
	return iban[:2], nil
}

func GetIBANChecksum(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("getIBANChecksum requires 1 argument")
	}
	iban, ok := args[0].(string)
	if !ok {
		return "", nil
	}
	iban = strings.ReplaceAll(iban, " ", "")
	if len(iban) < 4 {
		return "", nil
	}
	return iban[2:4], nil
}

func IsIPv4(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isIPv4 requires 1 argument")
	}
	ipStr, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	ip := net.ParseIP(ipStr)
	return ip != nil && ip.To4() != nil, nil
}

func IsIPv6(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isIPv6 requires 1 argument")
	}
	ipStr, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	ip := net.ParseIP(ipStr)
	return ip != nil && ip.To16() != nil && ip.To4() == nil, nil
}

func IsPrivateIP(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return false, fmt.Errorf("isPrivateIP requires 1 argument")
	}
	ipStr, ok := args[0].(string)
	if !ok {
		return false, nil
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false, nil
	}
	return ip.IsPrivate(), nil
}

func GetIPVersion(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return 0, fmt.Errorf("getIPVersion requires 1 argument")
	}
	ipStr, ok := args[0].(string)
	if !ok {
		return 0, nil
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return 0, nil
	}
	if ip.To4() != nil {
		return 4.0, nil
	}
	return 6.0, nil
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	}
	return 0, false
}
