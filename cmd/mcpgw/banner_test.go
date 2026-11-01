package main

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
)

const bannerURL = "https://gw.example.com"

func renderBanner(version, url string, color bool) string {
	var buf bytes.Buffer
	writeBanner(&buf, i18n.EN, version, url, color)
	return buf.String()
}

func TestWriteBannerWithoutColorHasNoEscapes(t *testing.T) {
	out := renderBanner("v1.2.3", bannerURL, false)
	if strings.Contains(out, "\x1b") {
		t.Fatalf("unexpected ESC byte in plain banner: %q", out)
	}
}

func TestWriteBannerListsEndpoints(t *testing.T) {
	out := renderBanner("v1.2.3", bannerURL, false)
	for _, want := range []string{
		"listening on " + bannerURL,
		"admin   " + bannerURL + "/admin/",
		"portal  " + bannerURL + "/portal/",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("banner missing %q in %q", want, out)
		}
	}
}

func TestWriteBannerTrimsTrailingSlash(t *testing.T) {
	out := renderBanner("", bannerURL+"/", false)
	if strings.Contains(out, "//admin/") || !strings.Contains(out, bannerURL+"/admin/") {
		t.Fatalf("trailing slash not trimmed: %q", out)
	}
}

func TestWriteBannerWithColorResetsEveryEscape(t *testing.T) {
	out := renderBanner("v1.2.3", bannerURL, true)
	opens := regexp.MustCompile(`\x1b\[(?:[0-9;]+)m`).FindAllString(out, -1)
	if len(opens) == 0 {
		t.Fatal("expected ANSI escapes with color on")
	}
	resets := strings.Count(out, ansiReset)
	if resets*2 != len(opens) {
		t.Fatalf("each style must be followed by a reset: %d sequences, %d resets", len(opens), resets)
	}
}

func TestWriteBannerWithColorKeepsPlainText(t *testing.T) {
	out := renderBanner("v1.2.3", bannerURL, true)
	plain := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(out, "")
	if plain != renderBanner("v1.2.3", bannerURL, false) {
		t.Fatalf("stripped colored banner differs from plain banner: %q", plain)
	}
}

func TestWriteBannerShowsVersionWhenSet(t *testing.T) {
	if out := renderBanner("v1.2.3", bannerURL, false); !strings.Contains(out, "MCP gateway v1.2.3") {
		t.Fatalf("version missing: %q", out)
	}
}

func TestWriteBannerOmitsStrayVersionWhenEmpty(t *testing.T) {
	out := renderBanner("", bannerURL, false)
	if !strings.Contains(out, "MCP gateway\n") {
		t.Fatalf("title should end after 'MCP gateway': %q", out)
	}
}

func TestWriteBannerOnlyEmitsGivenURL(t *testing.T) {
	out := renderBanner("v1.2.3", bannerURL, false)
	rest := strings.ReplaceAll(out, bannerURL, "")
	if strings.Contains(rest, "http") || strings.Contains(rest, "://") {
		t.Fatalf("banner leaked another URL: %q", out)
	}
}

func TestWriteBannerInPortuguese(t *testing.T) {
	var buf bytes.Buffer
	writeBanner(&buf, i18n.PTBR, "v1.2.3", bannerURL, false)
	for _, want := range []string{"gateway MCP v1.2.3", "banco de dados conectado", "ouvindo em " + bannerURL} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("pt banner missing %q in %q", want, buf.String())
		}
	}
}
