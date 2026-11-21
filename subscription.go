package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Node is one proxy server, already converted to a sing-box outbound.
type Node struct {
	ID     string
	Name   string
	Type   string // short label shown in the UI
	Server string
	Port   int
	UDP    bool // QUIC based: cannot be probed with a plain TCP dial
	Out    map[string]any
	Link   string // original share link (what we persist)
	Geo    *Geo   // location guessed from the node name (nil = unknown)
	Src    string // profile this server came from
}

func anyStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	}
	return ""
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func truthy(s string) bool {
	s = strings.ToLower(s)
	return s == "1" || s == "true"
}

func b64decode(s string) ([]byte, bool) {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)
	s = strings.TrimRight(s, "=")
	for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, true
		}
	}
	return nil, false
}

func mk(label, outType, name, server string, port int, udp bool, out map[string]any, link string) Node {
	out["type"] = outType
	out["server"] = server
	out["server_port"] = port
	if name == "" {
		name = fmt.Sprintf("%s:%d", server, port)
	}
	return Node{Name: name, Type: label, Server: server, Port: port, UDP: udp, Out: out, Link: link}
}

func buildTransport(netw, host, path, headerType, service string) (map[string]any, error) {
	if path == "" {
		path = "/"
	}
	switch netw {
	case "", "tcp":
		if headerType == "http" {
			m := map[string]any{"type": "http", "path": path}
			if h := splitList(host); len(h) > 0 {
				m["host"] = h
			}
			return m, nil
		}
		return nil, nil
	case "ws":
		m := map[string]any{"type": "ws", "path": path}
		if host != "" {
			m["headers"] = map[string]any{"Host": host}
		}
		return m, nil
	case "grpc":
		return map[string]any{"type": "grpc", "service_name": service}, nil
	case "h2", "http":
		m := map[string]any{"type": "http", "path": path}
		if h := splitList(host); len(h) > 0 {
			m["host"] = h
		}
		return m, nil
	case "httpupgrade":
		m := map[string]any{"type": "httpupgrade", "path": path}
		if host != "" {
			m["host"] = host
		}
		return m, nil
	}
	return nil, fmt.Errorf("unsupported transport %q", netw)
}

func tlsFromQuery(q url.Values, server, defSec string) map[string]any {
	sec := first(q.Get("security"), defSec)
	if sec != "tls" && sec != "reality" {
		return nil
	}
	t := map[string]any{"enabled": true, "server_name": first(q.Get("sni"), q.Get("peer"), q.Get("host"), server)}
	if truthy(q.Get("allowInsecure")) || truthy(q.Get("insecure")) {
		t["insecure"] = true
	}
	if a := splitList(q.Get("alpn")); len(a) > 0 {
		t["alpn"] = a
	}
	fp := q.Get("fp")
	if sec == "reality" {
		t["reality"] = map[string]any{"enabled": true, "public_key": q.Get("pbk"), "short_id": q.Get("sid")}
		if fp == "" {
			fp = "chrome"
		}
	}
	if fp != "" {
		t["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}
	return t
}

func parseVmess(line string) (Node, error) {
	b, ok := b64decode(strings.TrimPrefix(line, "vmess://"))
	if !ok {
		return Node{}, errors.New("bad vmess base64")
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		return Node{}, err
	}
	server := anyStr(v["add"])
	port, _ := strconv.Atoi(anyStr(v["port"]))
	id := anyStr(v["id"])
	if server == "" || port == 0 || id == "" {
		return Node{}, errors.New("incomplete vmess")
	}
	aid, _ := strconv.Atoi(anyStr(v["aid"]))
	out := map[string]any{"uuid": id, "security": first(anyStr(v["scy"]), "auto"), "alter_id": aid}
	host, path := anyStr(v["host"]), anyStr(v["path"])
	if anyStr(v["tls"]) == "tls" {
		t := map[string]any{"enabled": true, "server_name": first(anyStr(v["sni"]), host, server)}
		if a := splitList(anyStr(v["alpn"])); len(a) > 0 {
			t["alpn"] = a
		}
		if fp := anyStr(v["fp"]); fp != "" {
			t["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		}
		out["tls"] = t
	}
	tr, err := buildTransport(first(anyStr(v["net"]), "tcp"), host, path, anyStr(v["type"]), path)
	if err != nil {
		return Node{}, err
	}
	if tr != nil {
		out["transport"] = tr
	}
	return mk("vmess", "vmess", anyStr(v["ps"]), server, port, false, out, line), nil
}

func parseSS(line string) (Node, error) {
	raw := strings.TrimPrefix(line, "ss://")
	name := ""
	if i := strings.LastIndex(raw, "#"); i >= 0 {
		name = unescape(raw[i+1:])
		raw = raw[:i]
	}
	plugin := ""
	if i := strings.Index(raw, "?"); i >= 0 {
		q, _ := url.ParseQuery(raw[i+1:])
		plugin = q.Get("plugin")
		raw = raw[:i]
	}
	raw = strings.TrimSuffix(raw, "/")
	var userinfo, hostport string
	if at := strings.LastIndex(raw, "@"); at >= 0 {
		userinfo, hostport = raw[:at], raw[at+1:]
		if b, ok := b64decode(userinfo); ok && strings.Contains(string(b), ":") {
			userinfo = string(b)
		} else {
			userinfo = unescape(userinfo)
		}
	} else {
		b, ok := b64decode(raw)
		if !ok {
			return Node{}, errors.New("bad ss base64")
		}
		s := string(b)
		at := strings.LastIndex(s, "@")
		if at < 0 {
			return Node{}, errors.New("bad ss payload")
		}
		userinfo, hostport = s[:at], s[at+1:]
	}
	mp := strings.SplitN(userinfo, ":", 2)
	if len(mp) != 2 || mp[0] == "" {
		return Node{}, errors.New("bad ss credentials")
	}
	host, ps, err := splitHostPort(hostport)
	if err != nil {
		return Node{}, err
	}
	out := map[string]any{"method": mp[0], "password": mp[1]}
	if plugin != "" {
		parts := strings.SplitN(plugin, ";", 2)
		pn := parts[0]
		if pn == "simple-obfs" {
			pn = "obfs-local"
		}
		out["plugin"] = pn
		if len(parts) == 2 {
			out["plugin_opts"] = parts[1]
		}
	}
	return mk("ss", "shadowsocks", name, host, ps, false, out, line), nil
}

func splitHostPort(hp string) (string, int, error) {
	hp = strings.TrimSuffix(hp, "/")
	i := strings.LastIndex(hp, ":")
	if i < 0 {
		return "", 0, errors.New("missing port")
	}
	host := strings.Trim(hp[:i], "[]")
	port, err := strconv.Atoi(hp[i+1:])
	if err != nil || host == "" || port <= 0 || port > 65535 {
		return "", 0, errors.New("bad host:port")
	}
	return host, port, nil
}

func unescape(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

func parseURLNode(line string) (Node, error) {
	link := line
	name := ""
	if i := strings.LastIndex(line, "#"); i >= 0 {
		name = unescape(line[i+1:])
		line = line[:i]
	}
	u, err := url.Parse(line)
	if err != nil {
		return Node{}, err
	}
	q := u.Query()
	server := u.Hostname()
	port, _ := strconv.Atoi(u.Port())
	user := ""
	pass, hasPass := "", false
	if u.User != nil {
		user = u.User.Username()
		pass, hasPass = u.User.Password()
	}
	switch u.Scheme {
	case "vless":
		if user == "" || server == "" || port == 0 {
			return Node{}, errors.New("incomplete vless")
		}
		out := map[string]any{"uuid": user, "packet_encoding": "xudp"}
		if f := q.Get("flow"); f != "" {
			out["flow"] = f
		}
		if t := tlsFromQuery(q, server, ""); t != nil {
			out["tls"] = t
		}
		tr, err := buildTransport(q.Get("type"), q.Get("host"), q.Get("path"), q.Get("headerType"), q.Get("serviceName"))
		if err != nil {
			return Node{}, err
		}
		if tr != nil {
			out["transport"] = tr
		}
		return mk("vless", "vless", name, server, port, false, out, link), nil

	case "trojan":
		pw := user
		if hasPass {
			pw = user + ":" + pass
		}
		if pw == "" || server == "" || port == 0 {
			return Node{}, errors.New("incomplete trojan")
		}
		out := map[string]any{"password": pw}
		if t := tlsFromQuery(q, server, "tls"); t != nil {
			out["tls"] = t
		}
		tr, err := buildTransport(q.Get("type"), q.Get("host"), q.Get("path"), q.Get("headerType"), q.Get("serviceName"))
		if err != nil {
			return Node{}, err
		}
		if tr != nil {
			out["transport"] = tr
		}
		return mk("trojan", "trojan", name, server, port, false, out, link), nil

	case "hysteria2", "hy2":
		pw := user
		if hasPass {
			pw = user + ":" + pass
		}
		if port == 0 {
			port = 443
		}
		if pw == "" || server == "" {
			return Node{}, errors.New("incomplete hysteria2")
		}
		t := map[string]any{"enabled": true, "server_name": first(q.Get("sni"), server)}
		if truthy(q.Get("insecure")) || truthy(q.Get("allowInsecure")) {
			t["insecure"] = true
		}
		if a := splitList(q.Get("alpn")); len(a) > 0 {
			t["alpn"] = a
		}
		out := map[string]any{"password": pw, "tls": t}
		if q.Get("obfs") == "salamander" {
			out["obfs"] = map[string]any{"type": "salamander", "password": q.Get("obfs-password")}
		}
		return mk("hy2", "hysteria2", name, server, port, true, out, link), nil

	case "tuic":
		if user == "" || server == "" || port == 0 {
			return Node{}, errors.New("incomplete tuic")
		}
		t := map[string]any{"enabled": true, "server_name": first(q.Get("sni"), server)}
		if truthy(q.Get("allow_insecure")) || truthy(q.Get("insecure")) || truthy(q.Get("allowInsecure")) {
			t["insecure"] = true
		}
		if a := splitList(q.Get("alpn")); len(a) > 0 {
			t["alpn"] = a
		}
		out := map[string]any{"uuid": user, "password": pass, "tls": t}
		if c := q.Get("congestion_control"); c != "" {
			out["congestion_control"] = c
		}
		if m := q.Get("udp_relay_mode"); m != "" {
			out["udp_relay_mode"] = m
		}
		return mk("tuic", "tuic", name, server, port, true, out, link), nil
	}
	return Node{}, errors.New("unsupported scheme")
}

var jsonOutboundLabels = map[string]string{
	"vmess": "vmess", "vless": "vless", "trojan": "trojan",
	"shadowsocks": "ss", "hysteria2": "hy2", "tuic": "tuic",
}

func parseJSONLink(link string) (Node, error) {
	var ob map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(link, "json:")), &ob); err != nil {
		return Node{}, err
	}
	typ := anyStr(ob["type"])
	label, ok := jsonOutboundLabels[typ]
	if !ok {
		return Node{}, errors.New("unsupported outbound")
	}
	name := anyStr(ob["tag"])
	delete(ob, "tag")
	port, _ := strconv.Atoi(anyStr(ob["server_port"]))
	server := anyStr(ob["server"])
	if server == "" || port == 0 {
		return Node{}, errors.New("incomplete outbound")
	}
	n := Node{Name: first(name, fmt.Sprintf("%s:%d", server, port)), Type: label, Server: server, Port: port,
		UDP: typ == "hysteria2" || typ == "tuic", Out: ob, Link: link}
	return n, nil
}

func parseLink(line string) (Node, error) {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "vmess://"):
		return parseVmess(line)
	case strings.HasPrefix(line, "ss://"):
		return parseSS(line)
	case strings.HasPrefix(line, "vless://"), strings.HasPrefix(line, "trojan://"),
		strings.HasPrefix(line, "hysteria2://"), strings.HasPrefix(line, "hy2://"),
		strings.HasPrefix(line, "tuic://"):
		return parseURLNode(line)
	case strings.HasPrefix(line, "json:"):
		return parseJSONLink(line)
	}
	return Node{}, errors.New("unsupported link")
}

func nodesFromSingboxJSON(b []byte) []Node {
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return nil
	}
	var nodes []Node
	for _, ob := range cfg.Outbounds {
		raw, err := json.Marshal(ob)
		if err != nil {
			continue
		}
		if n, err := parseJSONLink("json:" + string(raw)); err == nil {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

func assignIDs(nodes []Node) {
	for i := range nodes {
		nodes[i].ID = fmt.Sprintf("n%03d", i+1)
		nodes[i].Geo = guessLocation(nodes[i].Name)
	}
}

func parseBody(body string) ([]Node, error) {
	s := strings.TrimSpace(strings.TrimPrefix(body, "\ufeff"))
	if strings.HasPrefix(s, "{") {
		nodes := nodesFromSingboxJSON([]byte(s))
		if len(nodes) == 0 {
			return nil, errors.New("no usable outbounds in that JSON")
		}
		assignIDs(nodes)
		return nodes, nil
	}
	if !strings.Contains(s, "://") {
		if b, ok := b64decode(s); ok {
			s = string(b)
		}
	}
	if looksLikeClash(s) {
		return parseClash(s)
	}
	var nodes []Node
	skipped := 0
	for _, line := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n, err := parseLink(line)
		if err != nil {
			skipped++
			continue
		}
		nodes = append(nodes, n)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no supported nodes found (%d lines skipped)", skipped)
	}
	assignIDs(nodes)
	return nodes, nil
}

func httpGet(u, ua string) ([]byte, error) {
	c := &http.Client{Timeout: 25 * time.Second}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("subscription server answered HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

func isURLInput(in string) bool {
	return !strings.ContainsAny(in, "\r\n") && (strings.HasPrefix(in, "http://") || strings.HasPrefix(in, "https://"))
}

// loadSubscription accepts a subscription URL or pasted share links.
func loadSubscription(input string) ([]Node, error) {
	in := strings.TrimSpace(input)
	if in == "" {
		return nil, errors.New("paste a subscription link first")
	}
	if !isURLInput(in) {
		return parseBody(in)
	}
	b, err := httpGet(in, "v2rayN/7.2.0")
	if err != nil {
		return nil, err
	}
	nodes, perr := parseBody(string(b))
	if perr == nil {
		return nodes, nil
	}
	// some providers only hand out a usable list to Clash clients
	if b2, err2 := httpGet(in, "clash.meta"); err2 == nil {
		if n2, e2 := parseBody(string(b2)); e2 == nil {
			return n2, nil
		}
	}
	return nil, perr
}

func nodesFromLinks(links []string) []Node {
	var nodes []Node
	for _, l := range links {
		if n, err := parseLink(l); err == nil {
			nodes = append(nodes, n)
		}
	}
	assignIDs(nodes)
	return nodes
}
