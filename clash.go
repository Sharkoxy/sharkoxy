package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ---- small helpers over the loosely typed YAML tree ----------------------------------

func cs(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func cm(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		if v, ok := m[k].(map[string]any); ok {
			return v
		}
	}
	return nil
}

// cl reads a list (or a comma separated string).
func cl(m map[string]any, keys ...string) []string {
	for _, k := range keys {
		switch v := m[k].(type) {
		case []any:
			var out []string
			for _, x := range v {
				if s, ok := x.(string); ok && s != "" {
					out = append(out, s)
				}
			}
			if len(out) > 0 {
				return out
			}
		case string:
			if l := splitList(v); len(l) > 0 {
				return l
			}
		}
	}
	return nil
}

func clashTLS(m map[string]any, server string, force bool) map[string]any {
	ro := cm(m, "reality-opts")
	if !force && !truthy(cs(m, "tls")) && ro == nil {
		return nil
	}
	t := map[string]any{"enabled": true, "server_name": first(cs(m, "servername", "sni"), server)}
	if truthy(cs(m, "skip-cert-verify")) {
		t["insecure"] = true
	}
	if a := cl(m, "alpn"); len(a) > 0 {
		t["alpn"] = a
	}
	fp := cs(m, "client-fingerprint")
	if ro != nil {
		t["reality"] = map[string]any{"enabled": true, "public_key": cs(ro, "public-key"), "short_id": cs(ro, "short-id")}
		if fp == "" {
			fp = "chrome"
		}
	}
	if fp != "" {
		t["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}
	return t
}

func clashTransport(m map[string]any) (map[string]any, error) {
	switch netw := cs(m, "network"); netw {
	case "", "tcp":
		return nil, nil
	case "ws":
		o := cm(m, "ws-opts", "ws-opt")
		host := first(cs(cm(o, "headers"), "Host", "host"), cs(cm(m, "ws-headers"), "Host"))
		return buildTransport("ws", host, first(cs(o, "path"), cs(m, "ws-path")), "", "")
	case "grpc":
		return buildTransport("grpc", "", "", "", cs(cm(m, "grpc-opts", "grpc-opt"), "grpc-service-name"))
	case "h2":
		o := cm(m, "h2-opts", "h2-opt")
		return buildTransport("h2", strings.Join(cl(o, "host"), ","), cs(o, "path"), "", "")
	case "http":
		o := cm(m, "http-opts", "http-opt")
		path := ""
		if p := cl(o, "path"); len(p) > 0 {
			path = p[0]
		}
		return buildTransport("http", strings.Join(cl(cm(o, "headers"), "Host", "host"), ","), path, "", "")
	default:
		return nil, fmt.Errorf("unsupported transport %q", netw)
	}
}

func clashSSPlugin(m map[string]any, out map[string]any) {
	plugin := cs(m, "plugin")
	if plugin == "" {
		return
	}
	o := cm(m, "plugin-opts")
	switch plugin {
	case "obfs", "simple-obfs", "obfs-local":
		out["plugin"] = "obfs-local"
		opts := "obfs=" + first(cs(o, "mode"), "http")
		if h := cs(o, "host"); h != "" {
			opts += ";obfs-host=" + h
		}
		out["plugin_opts"] = opts
	case "v2ray-plugin":
		out["plugin"] = "v2ray-plugin"
		opts := "mode=" + first(cs(o, "mode"), "websocket")
		if truthy(cs(o, "tls")) {
			opts += ";tls"
		}
		if h := cs(o, "host"); h != "" {
			opts += ";host=" + h
		}
		if p := cs(o, "path"); p != "" {
			opts += ";path=" + p
		}
		out["plugin_opts"] = opts
	}
}

// clashToNode converts one Clash proxy entry into a node (via a sing-box outbound).
func clashToNode(m map[string]any) (Node, error) {
	typ := strings.ToLower(cs(m, "type"))
	server := cs(m, "server")
	port, _ := strconv.Atoi(cs(m, "port"))
	if server == "" || port == 0 {
		return Node{}, errors.New("incomplete proxy")
	}
	out := map[string]any{"tag": cs(m, "name"), "server": server, "server_port": port}

	addTransport := func() error {
		tr, err := clashTransport(m)
		if err != nil {
			return err
		}
		if tr != nil {
			out["transport"] = tr
		}
		return nil
	}

	switch typ {
	case "ss":
		out["type"], out["method"], out["password"] = "shadowsocks", cs(m, "cipher", "method"), cs(m, "password")
		if out["method"] == "" {
			return Node{}, errors.New("ss without cipher")
		}
		clashSSPlugin(m, out)
	case "vmess":
		aid, _ := strconv.Atoi(cs(m, "alterId", "alter-id"))
		out["type"], out["uuid"], out["security"], out["alter_id"] = "vmess", cs(m, "uuid"), first(cs(m, "cipher"), "auto"), aid
		if t := clashTLS(m, server, false); t != nil {
			out["tls"] = t
		}
		if err := addTransport(); err != nil {
			return Node{}, err
		}
	case "vless":
		out["type"], out["uuid"], out["packet_encoding"] = "vless", cs(m, "uuid"), "xudp"
		if f := cs(m, "flow"); f != "" {
			out["flow"] = f
		}
		if t := clashTLS(m, server, false); t != nil {
			out["tls"] = t
		}
		if err := addTransport(); err != nil {
			return Node{}, err
		}
	case "trojan":
		out["type"], out["password"] = "trojan", cs(m, "password")
		if cs(m, "tls") != "false" {
			out["tls"] = clashTLS(m, server, true)
		}
		if err := addTransport(); err != nil {
			return Node{}, err
		}
	case "hysteria2", "hy2":
		out["type"], out["password"] = "hysteria2", cs(m, "password")
		out["tls"] = clashTLS(m, server, true)
		if cs(m, "obfs") == "salamander" {
			out["obfs"] = map[string]any{"type": "salamander", "password": cs(m, "obfs-password")}
		}
	case "tuic":
		out["type"], out["uuid"], out["password"] = "tuic", cs(m, "uuid"), cs(m, "password")
		out["tls"] = clashTLS(m, server, true)
		if c := cs(m, "congestion-controller", "congestion_control"); c != "" {
			out["congestion_control"] = c
		}
		if u := cs(m, "udp-relay-mode"); u != "" {
			out["udp_relay_mode"] = u
		}
	default:
		return Node{}, fmt.Errorf("unsupported proxy type %q", typ)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return Node{}, err
	}
	return parseJSONLink("json:" + string(raw))
}

func parseClash(text string) ([]Node, error) {
	items, err := clashProxies(text)
	if err != nil {
		return nil, err
	}
	var nodes []Node
	skipped := 0
	for _, it := range items {
		if n, err := clashToNode(it); err == nil {
			nodes = append(nodes, n)
		} else {
			skipped++
		}
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no supported nodes in that Clash profile (%d skipped)", skipped)
	}
	assignIDs(nodes)
	return nodes, nil
}
