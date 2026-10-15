package proxy

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/renja-g/RiftRelay/internal/testutil"
)

// benchLadderPage is a 205-entry league-v4 entries page, as Riot sends it.
func benchLadderPage() []byte {
	var b strings.Builder
	b.WriteByte('[')
	for i := range 205 {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"leagueId":"%08x-1d2c-4b8e-9f3a-%012x","queueType":"RANKED_SOLO_5x5","tier":"GOLD","rank":"IV","puuid":"%026x%026x%026x","leaguePoints":%d,"wins":%d,"losses":%d,"veteran":false,"inactive":false,"freshBlood":false,"hotStreak":%t}`,
			i*7919, i*104729, i*48271, i*16807, i*69621, i%100, 20+i%150, 18+i%140, i%7 == 0)
	}
	b.WriteByte(']')
	return []byte(b.String())
}

func benchGzip(body []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(body)
	_ = zw.Close()
	return buf.Bytes()
}

// BenchmarkProxyLadderPage measures the relay's per-request cost for a ladder
// page with an in-memory upstream, so only RiftRelay's own work is timed.
func BenchmarkProxyLadderPage(b *testing.B) {
	page := benchLadderPage()
	for _, tc := range []struct {
		name     string
		body     []byte
		encoding string
	}{
		{name: "gzip", body: benchGzip(page), encoding: "gzip"},
		{name: "identity", body: page},
	} {
		b.Run(tc.name, func(b *testing.B) {
			cfg := testutil.DummyConfig()
			handler := New(cfg, WithBaseTransport(testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
				header := http.Header{
					"Content-Type":   []string{"application/json;charset=utf-8"},
					"Content-Length": []string{strconv.Itoa(len(tc.body))},
				}
				if tc.encoding != "" {
					header.Set("Content-Encoding", tc.encoding)
				}
				resp := testutil.HTTPResponseBytes(http.StatusOK, tc.body, header)
				resp.ContentLength = int64(len(tc.body))
				return resp, nil
			})))

			b.SetBytes(int64(len(tc.body)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/euw1/lol/league/v4/entries/RANKED_SOLO_5x5/GOLD/IV?page=1", nil))
				if rec.Code != http.StatusOK {
					b.Fatalf("status = %d", rec.Code)
				}
				_, _ = io.Copy(io.Discard, rec.Body)
			}
		})
	}
}
