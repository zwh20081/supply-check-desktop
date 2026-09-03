package common

import (
	"net/url"
	"regexp"
	"strings"
)

// Evidence redaction. Probe evidence can quote upstream error bodies verbatim,
// so masking runs before it reaches a report or the PDF.

var (
	maskURLPattern    = regexp.MustCompile(`(http|https)://[^\s/$.?#].[^\s]*`)
	maskDomainPattern = regexp.MustCompile(`\b(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}\b`)
	maskIPPattern     = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	// maskApiKeyPattern matches patterns like 'api_key:xxx' or "api_key:xxx" to mask the API key value
	maskApiKeyPattern = regexp.MustCompile(`(['"]?)api_key:([^\s'"]+)(['"]?)`)
	// maskBearerPattern masks "Bearer <token>" in error text (e.g. echoed Authorization headers).
	maskBearerPattern = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._\-]{6,}`)
	// maskBasicPattern masks "Basic <base64>" — base64 credentials decode to a
	// usable user:password pair, so the payload must never survive into a report.
	maskBasicPattern = regexp.MustCompile(`(?i)\bbasic\s+[A-Za-z0-9+/=]{8,}`)
	// maskJWTPattern masks three-segment JSON Web Tokens. Relay error bodies
	// frequently echo the submitted bearer token verbatim.
	maskJWTPattern = regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{4,}`)
	// maskTokenPattern masks bare secret-shaped tokens that upstreams sometimes
	// echo back verbatim in error bodies (e.g. "Incorrect API key provided: sk-...").
	// Only very key-like shapes are matched to avoid redacting ordinary words.
	// Ordered longest-prefix-first so github_pat_ is not shadowed by a shorter rule.
	maskTokenPattern = regexp.MustCompile(`\b(github_pat_[A-Za-z0-9_]{20,}|gh[pousr]_[A-Za-z0-9]{16,}|xox[baprs]-[A-Za-z0-9\-]{10,}|sk-ant-[A-Za-z0-9_\-]{8,}|sk-[A-Za-z0-9_\-]{8,}|AIza[A-Za-z0-9_\-]{20,}|AKIA[A-Z0-9]{12,}|ASIA[A-Z0-9]{12,})`)
)

// maskTokenPrefixes maps a detected secret to the stub kept in evidence. Enough
// to identify WHICH credential family leaked, never enough to use it.
var maskTokenPrefixes = []string{
	"github_pat_", "ghp_", "gho_", "ghu_", "ghs_", "ghr_",
	"xoxb-", "xoxa-", "xoxp-", "xoxr-", "xoxs-",
	"sk-ant-", "sk-", "AIza", "AKIA", "ASIA",
}

func maskHostTail(parts []string) []string {
	if len(parts) < 2 {
		return parts
	}
	lastPart := parts[len(parts)-1]
	secondLastPart := parts[len(parts)-2]
	if len(lastPart) == 2 && len(secondLastPart) <= 3 {
		// Likely country code TLD like co.uk, com.cn
		return []string{secondLastPart, lastPart}
	}
	return []string{lastPart}
}

// maskHostForURL collapses subdomains and keeps only masked prefix + preserved tail.
// Example: api.openai.com -> ***.com, sub.domain.co.uk -> ***.co.uk
func maskHostForURL(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return "***"
	}
	tail := maskHostTail(parts)
	return "***." + strings.Join(tail, ".")
}

// maskHostForPlainDomain masks a plain domain and reflects subdomain depth with multiple ***.
// Example: openai.com -> ***.com, api.openai.com -> ***.***.com, sub.domain.co.uk -> ***.***.co.uk
func maskHostForPlainDomain(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return domain
	}
	tail := maskHostTail(parts)
	numStars := len(parts) - len(tail)
	if numStars < 1 {
		numStars = 1
	}
	stars := strings.TrimSuffix(strings.Repeat("***.", numStars), ".")
	return stars + "." + strings.Join(tail, ".")
}

// MaskSensitiveInfo masks sensitive information like URLs, IPs, and domain names in a string
// Example:
// http://example.com -> http://***.com
// https://api.test.org/v1/users/123?key=secret -> https://***.org/***/***/?key=***
// https://sub.domain.co.uk/path/to/resource -> https://***.co.uk/***/***
// 192.168.1.1 -> ***.***.***.***
// openai.com -> ***.com
// www.openai.com -> ***.***.com
// api.openai.com -> ***.***.com
func MaskSensitiveInfo(str string) string {
	// Mask URLs
	str = maskURLPattern.ReplaceAllStringFunc(str, func(urlStr string) string {
		u, err := url.Parse(urlStr)
		if err != nil {
			return urlStr
		}

		host := u.Host
		if host == "" {
			return urlStr
		}

		// Mask host with unified logic
		maskedHost := maskHostForURL(host)

		result := u.Scheme + "://" + maskedHost

		// Mask path
		if u.Path != "" && u.Path != "/" {
			pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")
			maskedPathParts := make([]string, len(pathParts))
			for i := range pathParts {
				if pathParts[i] != "" {
					maskedPathParts[i] = "***"
				}
			}
			if len(maskedPathParts) > 0 {
				result += "/" + strings.Join(maskedPathParts, "/")
			}
		} else if u.Path == "/" {
			result += "/"
		}

		// Mask query parameters
		if u.RawQuery != "" {
			values, err := url.ParseQuery(u.RawQuery)
			if err != nil {
				// If can't parse query, just mask the whole query string
				result += "?***"
			} else {
				maskedParams := make([]string, 0, len(values))
				for key := range values {
					maskedParams = append(maskedParams, key+"=***")
				}
				if len(maskedParams) > 0 {
					result += "?" + strings.Join(maskedParams, "&")
				}
			}
		}

		return result
	})

	// Mask domain names without protocol (like openai.com, www.openai.com)
	str = maskDomainPattern.ReplaceAllStringFunc(str, func(domain string) string {
		return maskHostForPlainDomain(domain)
	})

	// Mask IP addresses
	str = maskIPPattern.ReplaceAllString(str, "***.***.***.***")

	// Mask API keys (e.g., "api_key:AIzaSyAAAaUooTUni8AdaOkSRMda30n_Q4vrV70" -> "api_key:***")
	str = maskApiKeyPattern.ReplaceAllString(str, "${1}api_key:***${3}")

	// Mask echoed bearer tokens and bare secret-shaped keys (e.g. upstream error
	// bodies that quote the submitted credential verbatim).
	str = maskBearerPattern.ReplaceAllString(str, "Bearer ***")
	str = maskBasicPattern.ReplaceAllString(str, "Basic ***")
	str = maskJWTPattern.ReplaceAllString(str, "eyJ***")
	str = maskTokenPattern.ReplaceAllStringFunc(str, func(m string) string {
		for _, prefix := range maskTokenPrefixes {
			if strings.HasPrefix(m, prefix) {
				return prefix + "***"
			}
		}
		return "***"
	})

	return str
}

// TruncateRunes bounds a string by RUNE count. Byte slicing would split a
// multi-byte character and produce mojibake in the report, so evidence fields
// must use this instead of s[:n].
func TruncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
