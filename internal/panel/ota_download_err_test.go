package panel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
)

func TestFriendlyDownloadErr_networkNoURL(t *testing.T) {
	longURL := `Get "https://release-assets.githubusercontent.com/github-production-release-asset/1447918018/e4b38b55-9dec-4d94-83d7-870162bf4f88?sp=r&sv=2018-11-09&sig=abc": dial tcp: i/o timeout`
	cases := []error{
		errors.New(longURL),
		&net.DNSError{Err: "no such host", Name: "release-assets.githubusercontent.com", IsNotFound: true},
		fmt.Errorf("read: %w", io.ErrUnexpectedEOF),
		context.DeadlineExceeded,
		&timeoutErr{},
	}
	for _, in := range cases {
		out := friendlyDownloadErr(in)
		if out == nil {
			t.Fatalf("expected error for %v", in)
		}
		s := out.Error()
		if strings.Contains(s, "https://") || strings.Contains(s, "release-assets") {
			t.Fatalf("still contains URL/CDN noise: %q", s)
		}
		if !strings.Contains(s, "网络异常") {
			t.Fatalf("want 网络异常 hint, got %q", s)
		}
		if !strings.Contains(s, "手动下载") {
			t.Fatalf("want manual-download suggestion, got %q", s)
		}
	}
}

func TestFriendlyDownloadErr_passthroughShort(t *testing.T) {
	in := errors.New("access is denied")
	out := friendlyDownloadErr(in)
	if out.Error() != "access is denied" {
		t.Fatalf("got %q", out.Error())
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

var _ net.Error = timeoutErr{}
