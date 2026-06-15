// Package auth holds YouTube TV credentials and derives the SAPISIDHASH
// authorization header and Lounge XSRF token.
package auth

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/ygelfand/lib-yttv/constants"
)

type Creds struct {
	GoogleAccountID string `mapstructure:"google_account_id" yaml:"google_account_id" json:"google_account_id"`
	SAPISID         string `mapstructure:"sapisid" yaml:"sapisid" json:"sapisid"`
	Secure3PSID     string `mapstructure:"secure_3psid" yaml:"secure_3psid" json:"secure_3psid"`
}

func (c *Creds) Validate() error {
	var missing []string
	if c.SAPISID == "" {
		missing = append(missing, "sapisid")
	}
	if c.Secure3PSID == "" {
		missing = append(missing, "secure_3psid")
	}
	if len(missing) > 0 {
		return fmt.Errorf("auth: missing required fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

// sha1(google_account_id + " " + ts + " " + SAPISID + " " + Origin)
func (c *Creds) SAPISIDHash(ts int64) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s %d %s %s", c.GoogleAccountID, ts, c.SAPISID, constants.Origin)
	return hex.EncodeToString(h.Sum(nil))
}

func (c *Creds) AuthorizationHeader(ts int64) string {
	h := c.SAPISIDHash(ts)
	return fmt.Sprintf("SAPISIDHASH %d_%s_u SAPISID3PHASH %d_%s_u", ts, h, ts, h)
}

// base64("," + base64(SAPISID) + "," + base64(SAPISID))
func (c *Creds) LoungeXSRFToken() string {
	s := base64.StdEncoding.EncodeToString([]byte(c.SAPISID))
	return base64.StdEncoding.EncodeToString([]byte("," + s + "," + s))
}

var datasyncIDRE = regexp.MustCompile(`"DATASYNC_ID":"([^"|]+)`)

// DiscoverGoogleAccountID fetches the tv.youtube.com homepage and extracts
// DATASYNC_ID from the embedded ytcfg.
func (c *Creds) DiscoverGoogleAccountID(ctx context.Context, hc *http.Client) (string, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, constants.Origin+"/", nil)
	if err != nil {
		return "", err
	}
	c.AddCookies(req)
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	m := datasyncIDRE.FindSubmatch(body)
	if m == nil {
		return "", errors.New("auth: DATASYNC_ID not found in tv.youtube.com response (cookies expired?)")
	}
	return string(m[1]), nil
}

func (c *Creds) AddCookies(req *http.Request) {
	req.AddCookie(&http.Cookie{Name: "__Secure-3PAPISID", Value: c.SAPISID})
	req.AddCookie(&http.Cookie{Name: "__Secure-3PSID", Value: c.Secure3PSID})
}
