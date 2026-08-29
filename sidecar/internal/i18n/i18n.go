package i18n

import (
	"embed"
	"sync"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

// Translation runtime for the PDF renderer: Init plus Translate. Reports are
// always rendered in one explicitly chosen language, so there is no
// request-context language negotiation or user-preference lookup.

const (
	LangZhCN    = "zh-CN"
	LangZhTW    = "zh-TW"
	LangEn      = "en"
	LangFr      = "fr"
	LangRu      = "ru"
	LangJa      = "ja"
	LangVi      = "vi"
	DefaultLang = LangEn
)

//go:embed locales/*.yaml
var localeFS embed.FS

var (
	bundle     *i18n.Bundle
	localizers = make(map[string]*i18n.Localizer)
	mu         sync.RWMutex
	initOnce   sync.Once
)

// Init loads the embedded locale files. Safe to call more than once.
func Init() error {
	var initErr error
	initOnce.Do(func() {
		bundle = i18n.NewBundle(language.Chinese)
		bundle.RegisterUnmarshalFunc("yaml", yaml.Unmarshal)
		for _, lang := range []string{LangZhCN, LangZhTW, LangEn, LangFr, LangRu, LangJa, LangVi} {
			if _, err := bundle.LoadMessageFileFS(localeFS, "locales/"+lang+".yaml"); err != nil {
				initErr = err
				return
			}
			localizers[lang] = i18n.NewLocalizer(bundle, lang)
		}
	})
	return initErr
}

// GetLocalizer returns the localizer for lang, falling back to DefaultLang.
func GetLocalizer(lang string) *i18n.Localizer {
	mu.RLock()
	defer mu.RUnlock()
	if loc, ok := localizers[lang]; ok {
		return loc
	}
	if loc, ok := localizers[DefaultLang]; ok {
		return loc
	}
	return i18n.NewLocalizer(bundle, DefaultLang)
}

// Translate resolves key for lang, substituting args into the template. It
// returns the key itself when no translation exists, matching upstream.
func Translate(lang, key string, args ...map[string]any) string {
	config := &i18n.LocalizeConfig{MessageID: key}
	if len(args) > 0 && args[0] != nil {
		config.TemplateData = args[0]
	}
	msg, err := GetLocalizer(lang).Localize(config)
	if err != nil {
		return key
	}
	return msg
}
