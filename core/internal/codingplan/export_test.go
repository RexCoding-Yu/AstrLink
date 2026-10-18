package codingplan

import "time"

func (fetcher *Fetcher) SetClock(now func() time.Time) { fetcher.now = now }
