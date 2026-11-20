package positions

import "time"

type realTicker struct {
	ticker *time.Ticker
}

func newRealTicker(period time.Duration) Ticker {
	return realTicker{ticker: time.NewTicker(period)}
}

func (t realTicker) C() <-chan time.Time {
	return t.ticker.C
}

func (t realTicker) Stop() {
	t.ticker.Stop()
}
