package mitmproxy

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/AdguardTeam/AdGuardHome/filters"
	"github.com/AdguardTeam/golibs/jsonutil"
	"github.com/AdguardTeam/golibs/log"
)

// Print to log and set HTTP error message
func httpError(r *http.Request, w http.ResponseWriter, code int, format string, args ...interface{}) {
	text := fmt.Sprintf(format, args...)
	log.Info("MITM: %s %s: %s", r.Method, r.URL, text)
	http.Error(w, text, code)
}

type mitmConfigJSON struct {
	Enabled    bool   `json:"enabled"`
	ListenAddr string `json:"listen_address"`
	ListenPort int    `json:"listen_port"`

	UserName string `json:"auth_username"`
	Password string `json:"auth_password"`

	CertData string `json:"cert_data"`
	PKeyData string `json:"pkey_data"`
}

func (p *MITMProxy) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	resp := mitmConfigJSON{}
	p.confLock.Lock()
	resp.Enabled = p.conf.Enabled
	host, port, _ := net.SplitHostPort(p.conf.ListenAddr)
	resp.ListenAddr = host
	resp.ListenPort, _ = strconv.Atoi(port)
	resp.UserName = p.conf.UserName
	resp.Password = p.conf.Password
	p.confLock.Unlock()

	js, err := json.Marshal(resp)
	if err != nil {
		httpError(r, w, http.StatusInternalServerError, "json.Marshal: %s", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(js)
}

func (p *MITMProxy) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	req := mitmConfigJSON{}
	_, err := jsonutil.DecodeObject(&req, r.Body)
	if err != nil {
		httpError(r, w, http.StatusBadRequest, "json.Decode: %s", err)
		return
	}

	if !((len(req.CertData) != 0 && len(req.PKeyData) != 0) ||
		(len(req.CertData) == 0 && len(req.PKeyData) == 0)) {
		httpError(r, w, http.StatusBadRequest, "certificate & private key must be both empty or specified")
		return
	}

	p.confLock.Lock()
	if len(req.CertData) != 0 {
		err = p.storeCert([]byte(req.CertData), []byte(req.PKeyData))
		if err != nil {
			httpError(r, w, http.StatusInternalServerError, "%s", err)
			p.confLock.Unlock()
			return
		}
		p.conf.RegenCert = false
	} else {
		p.conf.RegenCert = true
	}
	p.conf.Enabled = req.Enabled
	p.conf.ListenAddr = net.JoinHostPort(req.ListenAddr, strconv.Itoa(req.ListenPort))
	p.conf.UserName = req.UserName
	p.conf.Password = req.Password
	p.confLock.Unlock()

	p.conf.ConfigModified()

	p.Close()
	err = p.Restart()
	if err != nil {
		httpError(r, w, http.StatusInternalServerError, "%s", err)
		return
	}
}

func (p *MITMProxy) handleFilterStatus(w http.ResponseWriter, r *http.Request) {
	type filterJSON struct {
		Enabled     bool      `json:"enabled"`
		Name        string    `json:"name"`
		URL         string    `json:"url"`
		RuleCount   uint64    `json:"rules_count"`
		LastUpdated time.Time `json:"last_updated"`
	}
	type respJSON struct {
		Filters []filterJSON `json:"filters"`
	}
	resp := respJSON{}

	filtrs := p.conf.Filter.List(0)
	for _, f := range filtrs {
		fj := filterJSON{
			Enabled:     f.Enabled,
			Name:        f.Name,
			URL:         f.URL,
			RuleCount:   f.RuleCount,
			LastUpdated: f.LastUpdated,
		}
		resp.Filters = append(resp.Filters, fj)
	}

	js, err := json.Marshal(resp)
	if err != nil {
		httpError(r, w, http.StatusInternalServerError, "json.Marshal: %s", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(js)
}

func (p *MITMProxy) handleFilterConfig(w http.ResponseWriter, r *http.Request) {
	type reqJSON struct {
		Enabled  bool   `json:"enabled"`
		Interval uint32 `json:"interval"`
	}
	req := reqJSON{}
	_, err := jsonutil.DecodeObject(&req, r.Body)
	if err != nil {
		httpError(r, w, http.StatusBadRequest, "json.Decode: %s", err)
		return
	}

	// Enabled
	// Interval

	p.conf.ConfigModified()
}

func (p *MITMProxy) handleFilterAdd(w http.ResponseWriter, r *http.Request) {
	type reqJSON struct {
		Name string `json:"name"`
		URL  string `json:"url"`
		Type string `json:"type"`
	}
	req := reqJSON{}
	_, err := jsonutil.DecodeObject(&req, r.Body)
	if err != nil {
		httpError(r, w, http.StatusBadRequest, "json.Decode: %s", err)
		return
	}

	switch req.Type {
	case "proxylist":
		//

	// case "blocklist":
	// case "whitelist":
	default:
		httpError(r, w, http.StatusBadRequest, "invalid type: %s", req.Type)
		return
	}

	f := filters.Filter{
		Enabled: true,
		Name:    req.Name,
		URL:     req.URL,
	}
	err = p.conf.Filter.Add(f)
	if err != nil {
		httpError(r, w, http.StatusBadRequest, "addFilter: %s", err)
		return
	}

	p.conf.ConfigModified()

	p.Close()
	err = p.Restart()
	if err != nil {
		httpError(r, w, http.StatusInternalServerError, "start: %s", err)
		return
	}
}

func (p *MITMProxy) handleFilterRemove(w http.ResponseWriter, r *http.Request) {
	type reqJSON struct {
		URL  string `json:"url"`
		Type string `json:"type"`
	}
	req := reqJSON{}
	_, err := jsonutil.DecodeObject(&req, r.Body)
	if err != nil {
		httpError(r, w, http.StatusBadRequest, "json.Decode: %s", err)
		return
	}

	switch req.Type {
	case "proxylist":
		//

	// case "blocklist":
	// case "whitelist":
	default:
		httpError(r, w, http.StatusBadRequest, "invalid type: %s", req.Type)
		return
	}

	removed := p.conf.Filter.Delete(req.URL)
	if removed == nil {
		httpError(r, w, http.StatusInternalServerError, "No filter with such URL")
		return
	}

	p.conf.ConfigModified()

	if removed.Enabled {
		p.Close()
	}

	err = os.Remove(removed.Path)
	if err != nil {
		log.Error("os.Remove: %s", err)
	}

	if removed.Enabled {
		err = p.Restart()
		if err != nil {
			httpError(r, w, http.StatusInternalServerError, "start: %s", err)
			return
		}
	}
}

func (p *MITMProxy) handleFilterModify(w http.ResponseWriter, r *http.Request) {
	type propsJSON struct {
		Name    string `json:"name"`
		URL     string `json:"url"`
		Enabled bool   `json:"enabled"`
	}
	type reqJSON struct {
		URL  string    `json:"url"`
		Type string    `json:"type"`
		Data propsJSON `json:"data"`
	}
	req := reqJSON{}
	_, err := jsonutil.DecodeObject(&req, r.Body)
	if err != nil {
		httpError(r, w, http.StatusBadRequest, "json.Decode: %s", err)
		return
	}

	switch req.Type {
	case "proxylist":
		//

	// case "blocklist":
	// case "whitelist":
	default:
		httpError(r, w, http.StatusBadRequest, "invalid type: %s", req.Type)
		return
	}

	st := p.conf.Filter.Modify(req.URL, req.Data.Enabled, req.Data.Name, req.Data.URL)
	if st == filters.StatusNotFound {
		httpError(r, w, http.StatusBadRequest, "filter not found: %s", req.URL)
		return
	}

	p.conf.ConfigModified()

	if st == filters.StatusChangedEnabled ||
		st == filters.StatusChangedURL {

		p.Close()
		err = p.Restart()
		if err != nil {
			httpError(r, w, http.StatusInternalServerError, "%s", err)
			return
		}
	}
}

func (p *MITMProxy) handleFilterRefresh(w http.ResponseWriter, r *http.Request) {
	type reqJSON struct {
		Type string `json:"type"`
	}
	req := reqJSON{}
	_, err := jsonutil.DecodeObject(&req, r.Body)
	if err != nil {
		httpError(r, w, http.StatusBadRequest, "json.Decode: %s", err)
		return
	}

	switch req.Type {
	case "proxylist":
		//

	// case "blocklist":
	// case "whitelist":
	default:
		httpError(r, w, http.StatusBadRequest, "invalid type: %s", req.Type)
		return
	}

	p.conf.Filter.Refresh(0)
}

// Initialize web handlers
func (p *MITMProxy) initWeb() {
	p.conf.HTTPRegister("GET", "/control/proxy_info", p.handleGetConfig)
	p.conf.HTTPRegister("POST", "/control/proxy_config", p.handleSetConfig)

	p.conf.HTTPRegister("GET", "/control/proxy_filter/status", p.handleFilterStatus)
	p.conf.HTTPRegister("POST", "/control/proxy_filter/config", p.handleFilterConfig)
	p.conf.HTTPRegister("POST", "/control/proxy_filter/add", p.handleFilterAdd)
	p.conf.HTTPRegister("POST", "/control/proxy_filter/remove", p.handleFilterRemove)
	p.conf.HTTPRegister("POST", "/control/proxy_filter/set", p.handleFilterModify)
	p.conf.HTTPRegister("POST", "/control/proxy_filter/refresh", p.handleFilterRefresh)
}
