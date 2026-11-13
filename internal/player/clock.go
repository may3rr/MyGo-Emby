package player

import "time"

// clockText is a playback time as the control bar shows it, "4:05" or
// "1:04:05", with "-" before a remaining time. The view asks for it every
// frame; it formats only when the whole second changes, so a frame that
// shows the same second allocates nothing.
type clockText struct {
	valid bool
	sec   int64
	neg   bool
	text  string
}

// at returns d as text, negative (remaining) when neg. d is floored to the
// second for elapsed times and rounded up for remaining ones, so that the
// two add up to the duration as a player's do.
func (t *clockText) at(d time.Duration, neg bool) string {
	if d < 0 {
		d = 0
	}
	sec := int64(d / time.Second)
	if neg && d%time.Second != 0 {
		sec++
	}
	if t.valid && t.sec == sec && t.neg == neg {
		return t.text
	}
	t.valid, t.sec, t.neg = true, sec, neg
	t.text = formatClock(sec, neg)
	return t.text
}

// formatClock formats whole seconds as m:ss or h:mm:ss.
func formatClock(sec int64, neg bool) string {
	var buf [24]byte
	i := len(buf)
	put2 := func(n int64) {
		i -= 2
		buf[i] = byte('0' + n/10)
		buf[i+1] = byte('0' + n%10)
	}
	h, m, s := sec/3600, sec/60%60, sec%60
	put2(s)
	i--
	buf[i] = ':'
	if h > 0 {
		put2(m)
		i--
		buf[i] = ':'
		for {
			i--
			buf[i] = byte('0' + h%10)
			h /= 10
			if h == 0 {
				break
			}
		}
	} else {
		if m >= 10 {
			put2(m)
		} else {
			i--
			buf[i] = byte('0' + m)
		}
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
