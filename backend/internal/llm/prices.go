package llm

import "strings"

// Usage — токены одного вызова, как их отдаёт API: Input — без кэша, чтение и
// запись кэша — отдельно.
type Usage struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
}

// price — доллары за миллион токенов.
type price struct {
	in, out, cacheRead float64
	// longIn/longOut — тариф запросов длиннее longOver токенов входа (0 — нет).
	longOver        int64
	longIn, longOut float64
}

// cacheWriteFactor — запись в кэш промпта (5 минут) дороже обычного входа.
const cacheWriteFactor = 1.25

// prices — тарифы Anthropic API на 2026-11-03 (справочник Claude API). Нужны
// только для своего учёта и потолка расходов; счёт выставляет провайдер. При
// смене модели в конфиге — сверить и дополнить. Неизвестная модель считается по
// самому дорогому тарифу: потолок сработает раньше, а не позже.
var prices = map[string]price{
	"claude-haiku-5-5":  {in: 0.10, out: 0.50, cacheRead: 0.01, longOver: 100_000, longIn: 0.50, longOut: 2.50},
	"claude-sonnet-5-5": {in: 2, out: 10, cacheRead: 0.10},
	"claude-opus-5-5":   {in: 4, out: 20, cacheRead: 0.20},
	"claude-fable-5-2":  {in: 8, out: 40, cacheRead: 0.20},
}

var fallbackPrice = prices["claude-fable-5-2"]

func priceOf(model string) price {
	if p, ok := prices[model]; ok {
		return p
	}
	// Снимок с датой («claude-sonnet-5-5-20261001») — по базовому имени.
	for name, p := range prices {
		if strings.HasPrefix(model, name) {
			return p
		}
	}
	return fallbackPrice
}

// Cost — стоимость вызова в микродолларах (миллионных долях доллара).
func Cost(model string, u Usage) int64 {
	p := priceOf(model)
	in, out := p.in, p.out
	if p.longOver > 0 && u.Input+u.CacheRead+u.CacheWrite > p.longOver {
		in, out = p.longIn, p.longOut
	}
	// (токены × $/млн) = микродоллары.
	usd := float64(u.Input)*in + float64(u.CacheWrite)*in*cacheWriteFactor + float64(u.CacheRead)*p.cacheRead + float64(u.Output)*out
	return int64(usd + 0.5)
}

func usdToMicro(usd float64) int64   { return int64(usd*1e6 + 0.5) }
func microToUSD(micro int64) float64 { return float64(micro) / 1e6 }
