package remux

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// serveSource lets FFmpeg seek the original file through the random token
// on this local server, keeping authentication queries and headers out of
// the child process's command line.
func (x *Session) serveSource(w http.ResponseWriter, r *http.Request) {
	if x.up == nil {
		http.NotFound(w, r)
		return
	}
	total := x.up.size()
	if total < 0 {
		if err := x.up.resolve(r.Context()); err != nil {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		total = x.up.size()
	}
	start, end, err := sourceRange(r.Header.Get("Range"), total)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", total))
		http.Error(w, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	h := w.Header()
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Type", "application/octet-stream")
	if total >= 0 {
		h.Set("Content-Length", strconv.FormatInt(end-start, 10))
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	resp, err := x.up.get(r.Context(), start, end)
	if err != nil {
		if err == io.EOF {
			http.Error(w, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		http.Error(w, "upstream read failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	status := http.StatusPartialContent
	if resp.StatusCode == http.StatusOK && start == 0 {
		status = http.StatusOK
	}
	if total >= 0 {
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end-1, total))
	}
	w.WriteHeader(status)
	_, _ = io.Copy(w, resp.Body)
}

func sourceRange(header string, total int64) (start, end int64, err error) {
	header = strings.TrimSpace(header)
	if header == "" {
		if total < 0 {
			return 0, 0, nil
		}
		return 0, total, nil
	}
	if !strings.HasPrefix(header, "bytes=") || strings.Contains(header, ",") {
		return 0, 0, fmt.Errorf("unsupported range")
	}
	a, b, ok := strings.Cut(strings.TrimPrefix(header, "bytes="), "-")
	if !ok || a == "" {
		return 0, 0, fmt.Errorf("unsupported suffix range")
	}
	start, err = strconv.ParseInt(strings.TrimSpace(a), 10, 64)
	if err != nil || start < 0 {
		return 0, 0, fmt.Errorf("bad range start")
	}
	if total >= 0 && start >= total {
		return 0, 0, fmt.Errorf("range starts after EOF")
	}
	if strings.TrimSpace(b) == "" {
		if total < 0 {
			return 0, 0, nil
		}
		return start, total, nil
	}
	last, err := strconv.ParseInt(strings.TrimSpace(b), 10, 64)
	if err != nil || last < start {
		return 0, 0, fmt.Errorf("bad range end")
	}
	end = last + 1
	if total >= 0 && end > total {
		end = total
	}
	if end <= start {
		return 0, 0, fmt.Errorf("empty range")
	}
	return start, end, nil
}
