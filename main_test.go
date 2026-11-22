package main

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestParseLinks(t *testing.T) {
	vmessJSON := `{"v":"2","ps":"HK vmess","add":"hk.example.com","port":"443","id":"11111111-1111-1111-1111-111111111111","aid":"0","scy":"auto","net":"ws","host":"cdn.example.com","path":"/ws","tls":"tls","sni":"cdn.example.com"}`
	links := []string{
		"vmess://" + b64(vmessJSON),
		"vless://22222222-2222-2222-2222-222222222222@jp.example.com:443?encryption=none&security=reality&sni=www.microsoft.com&fp=chrome&pbk=PUBKEY&sid=abcd&flow=xtls-rprx-vision&type=tcp#JP%20reality",
		"vless://33333333-3333-3333-3333-333333333333@us.example.com:8443?security=tls&sni=us.example.com&type=grpc&serviceName=svc#US grpc",
		"trojan://pass%40word@sg.example.com:443?sni=sg.example.com&type=ws&host=sg.example.com&path=%2Ftr#SG trojan",
		"ss://" + b64("aes-256-gcm:secret") + "@ss.example.com:8388#SS sip002",
		"ss://" + b64("chacha20-ietf-poly1305:pw@ss2.example.com:9000") + "#SS legacy",
		"hysteria2://hypass@hy.example.com:443?sni=hy.example.com&insecure=1&obfs=salamander&obfs-password=ob#HY2",
		"tuic://44444444-4444-4444-4444-444444444444:tpass@tuic.example.com:443?congestion_control=bbr&udp_relay_mode=native&alpn=h3&sni=tuic.example.com#TUIC",
		"garbage://nope",
		"vmess://not-base64!!",
	}
	body := ""
	for _, l := range links {
		body += l + "\n"
	}

	for name, in := range map[string]string{"plain": body, "base64": b64(body)} {
		nodes, err := parseBody(in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(nodes) != 8 {
			t.Fatalf("%s: want 8 nodes, got %d", name, len(nodes))
		}
		want := []struct{ typ, name, out string }{
			{"vmess", "HK vmess", "vmess"}, {"vless", "JP reality", "vless"}, {"vless", "US grpc", "vless"},
			{"trojan", "SG trojan", "trojan"}, {"ss", "SS sip002", "shadowsocks"}, {"ss", "SS legacy", "shadowsocks"},
			{"hy2", "HY2", "hysteria2"}, {"tuic", "TUIC", "tuic"},
		}
		for i, w := range want {
			n := nodes[i]
			if n.Type != w.typ || n.Name != w.name || n.Out["type"] != w.out {
				t.Errorf("%s[%d]: got type=%s name=%q out=%v", name, i, n.Type, n.Name, n.Out["type"])
			}
			if n.ID == "" || n.Server == "" || n.Port == 0 {
				t.Errorf("%s[%d]: missing id/server/port", name, i)
			}
		}
	}

	nodes, _ := parseBody(body)
	// spot-check details
	if tr := nodes[0].Out["transport"].(map[string]any); tr["type"] != "ws" || tr["path"] != "/ws" {
		t.Errorf("vmess ws transport wrong: %v", tr)
	}
	r := nodes[1].Out["tls"].(map[string]any)["reality"].(map[string]any)
	if r["public_key"] != "PUBKEY" || r["short_id"] != "abcd" || nodes[1].Out["flow"] != "xtls-rprx-vision" {
		t.Errorf("reality wrong: %v", nodes[1].Out)
	}
	if nodes[2].Out["transport"].(map[string]any)["service_name"] != "svc" {
		t.Errorf("grpc service name wrong")
	}
	if nodes[3].Out["password"] != "pass@word" {
		t.Errorf("trojan password decode: %v", nodes[3].Out["password"])
	}
	if nodes[4].Out["method"] != "aes-256-gcm" || nodes[4].Out["password"] != "secret" {
		t.Errorf("ss sip002 wrong: %v", nodes[4].Out)
	}
	if nodes[5].Server != "ss2.example.com" || nodes[5].Port != 9000 {
		t.Errorf("ss legacy wrong: %v", nodes[5])
	}
	if !nodes[6].UDP || !nodes[7].UDP || nodes[0].UDP {
		t.Errorf("UDP flags wrong")
	}
	if nodes[6].Out["obfs"].(map[string]any)["password"] != "ob" || nodes[6].Out["tls"].(map[string]any)["insecure"] != true {
		t.Errorf("hy2 wrong: %v", nodes[6].Out)
	}

	// persistence round trip
	var saved []string
	for _, n := range nodes {
		saved = append(saved, n.Link)
	}
	again := nodesFromLinks(saved)
	if len(again) != len(nodes) {
		t.Fatalf("round trip lost nodes: %d != %d", len(again), len(nodes))
	}
}

func TestSingboxJSONAndClash(t *testing.T) {
	js := `{"outbounds":[{"type":"direct","tag":"direct"},{"type":"trojan","tag":"A","server":"a.example.com","server_port":443,"password":"x"},{"type":"hysteria2","tag":"B","server":"b.example.com","server_port":443,"password":"y"}]}`
	nodes, err := parseBody(js)
	if err != nil || len(nodes) != 2 {
		t.Fatalf("json sub: %v %d", err, len(nodes))
	}
	if nodes[0].Name != "A" || nodes[1].Type != "hy2" || !nodes[1].UDP {
		t.Errorf("json nodes wrong: %+v", nodes)
	}
	if _, ok := nodes[0].Out["tag"]; ok {
		t.Errorf("tag should be stripped")
	}
	if _, err := parseBody("proxies:\n  - {name: a, type: ss}\n"); err == nil {
		t.Errorf("clash yaml should be rejected with a message")
	}
	if _, err := parseBody("hello world"); err == nil {
		t.Errorf("junk should error")
	}
}

func TestBuildConfig(t *testing.T) {
	nodes, _ := parseBody("trojan://p@a.example.com:443#A\ntrojan://p@b.example.com:443#B\n")
	for _, ver := range []string{"1.11.4", "1.12.0", "1.14.1", ""} {
		raw, err := buildConfig(nodes, defaultSettings(), 17890, 19090, "sec", ver)
		if err != nil {
			t.Fatal(err)
		}
		var cfg map[string]any
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatal(err)
		}
		outs := cfg["outbounds"].([]any)
		seen := map[string]bool{}
		for _, o := range outs {
			tag := o.(map[string]any)["tag"].(string)
			if seen[tag] {
				t.Fatalf("duplicate tag %s", tag)
			}
			seen[tag] = true
		}
		for _, need := range []string{"n001", "n002", "proxy", "direct"} {
			if !seen[need] {
				t.Errorf("v=%q missing outbound %s", ver, need)
			}
		}
		_, hasDNS := cfg["dns"]
		wantDNS := ver != "1.11.4"
		if hasDNS != wantDNS {
			t.Errorf("v=%q dns present=%v want %v", ver, hasDNS, wantDNS)
		}
		if cfg["route"].(map[string]any)["final"] != "proxy" {
			t.Errorf("route.final wrong")
		}
	}
	if !versionAtLeast("1.12.0", 1, 12) || versionAtLeast("1.11.9", 1, 12) || !versionAtLeast("2.0.0", 1, 12) || !versionAtLeast("1.13.0-beta.2", 1, 12) {
		t.Errorf("versionAtLeast wrong")
	}
}

func TestGuessLocation(t *testing.T) {
	cases := map[string]string{
		"🇭🇰 HK-01 IPLC":    "Hong Kong",
		"香港 02 | 高速":       "Hong Kong",
		"JP-Tokyo-03":      "Tokyo",
		"日本大阪 01":          "Osaka",
		"US Los Angeles 5": "Los Angeles",
		"美国 圣何塞":           "San Jose",
		"SG01":             "Singapore",
		"🇩🇪 Frankfurt":     "Frankfurt",
		"Taiwan TW 02":     "Taipei",
		"[IEPL] 新加坡 3":     "Singapore",
		"KR Seoul":         "Seoul",
		"UK London 2":      "London",
		"英国伦敦":             "London",
		"🇺🇸 premium":       "United States",
		"Netherlands-AMS":  "Amsterdam",
	}
	for name, want := range cases {
		g := guessLocation(name)
		if g == nil || g.Place != want {
			t.Errorf("%q -> %v, want %s", name, g, want)
		}
	}
	for _, name := range []string{"node-12", "Premium VIP", "climate 01", "x"} {
		if g := guessLocation(name); g != nil {
			t.Errorf("%q should be unknown, got %s", name, g.Place)
		}
	}
}

func TestCleanDomainsAndRules(t *testing.T) {
	got := cleanDomains("https://School.edu.cn/path\n*.company.com, .foo.org\n\nfoo.org example.com:8080")
	want := []string{"school.edu.cn", "company.com", "foo.org", "example.com"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v want %v", got, want)
		}
	}
	nodes, _ := parseBody("trojan://p@a.example.com:443#A\n")
	set := defaultSettings()
	set.LAN = true
	set.DirectList = "school.edu.cn"
	set.ProxyList = "keepme.cn"
	raw, err := buildConfig(nodes, set, 1, 2, "s", "1.12.0")
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(raw, &cfg)
	if cfg["inbounds"].([]any)[0].(map[string]any)["listen"] != "0.0.0.0" {
		t.Errorf("LAN listen wrong")
	}
	rules := cfg["route"].(map[string]any)["rules"].([]any)
	// order matters: private, proxy-list, direct-list, built-in bypass
	if len(rules) != 4 || rules[1].(map[string]any)["outbound"] != "proxy" || rules[2].(map[string]any)["outbound"] != "direct" {
		t.Errorf("rule order wrong: %v", rules)
	}
}

const clashFlow = `# generated by a converter
port: 7890
socks-port: 7891
allow-lan: false
mode: rule
proxies:
- {name: "🇭🇰 HK 01", type: ss, server: hk1.example.com, port: 8388, cipher: aes-256-gcm, password: "p@ss: word", udp: true}
- {name: "🇯🇵 JP vmess ws", type: vmess, server: jp.example.com, port: 443, uuid: 11111111-1111-1111-1111-111111111111, alterId: 0, cipher: auto, tls: true, servername: cdn.example.com, skip-cert-verify: true, network: ws, ws-opts: {path: /ws, headers: {Host: cdn.example.com}}}
- {name: "\U0001F1F8\U0001F1EC SG trojan", type: trojan, server: sg.example.com, port: 443, password: tpass, sni: sg.example.com, udp: true, alpn: [h2, http/1.1]}
- {name: US reality, type: vless, server: 1.2.3.4, port: 443, uuid: 22222222-2222-2222-2222-222222222222, flow: xtls-rprx-vision, tls: true, servername: www.microsoft.com, client-fingerprint: chrome, reality-opts: {public-key: PUBKEY, short-id: abcd}, network: tcp}
- {name: DE hy2, type: hysteria2, server: de.example.com, port: 443, password: hpass, sni: de.example.com, obfs: salamander, obfs-password: ob}
- {name: TW tuic, type: tuic, server: tw.example.com, port: 443, uuid: 33333333-3333-3333-3333-333333333333, password: tp, congestion-controller: bbr, udp-relay-mode: native, alpn: [h3]}
- {name: old ssr, type: ssr, server: x.example.com, port: 1, cipher: a, password: b}
proxy-groups:
- {name: PROXY, type: select, proxies: ["🇭🇰 HK 01", DIRECT]}
rules:
- DOMAIN-SUFFIX,google.com,PROXY
- MATCH,DIRECT
`

const clashBlock = `proxies:
  - name: "KR block"
    type: vmess
    server: kr.example.com
    port: 443
    uuid: 44444444-4444-4444-4444-444444444444
    alterId: 0
    cipher: auto
    tls: true
    network: grpc
    grpc-opts:
      grpc-service-name: svc   # comment
  - name: UK ss obfs
    type: ss
    server: uk.example.com
    port: 8443
    cipher: chacha20-ietf-poly1305
    password: pw
    plugin: obfs
    plugin-opts:
      mode: tls
      host: bing.com
  - name: NL ws
    type: trojan
    server: nl.example.com
    port: 443
    password: x
    network: ws
    ws-opts:
      path: /tr
      headers:
        Host: nl.example.com
proxy-groups:
  - name: Auto
    type: url-test
    proxies:
      - KR block
`

func TestClashFlowStyle(t *testing.T) {
	nodes, err := parseBody(clashFlow)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 6 {
		t.Fatalf("want 6 nodes (ssr skipped), got %d", len(nodes))
	}
	if nodes[0].Name != "🇭🇰 HK 01" || nodes[0].Out["password"] != "p@ss: word" || nodes[0].Out["method"] != "aes-256-gcm" {
		t.Errorf("ss wrong: %+v", nodes[0].Out)
	}
	tr := nodes[1].Out["transport"].(map[string]any)
	if tr["type"] != "ws" || tr["path"] != "/ws" || tr["headers"].(map[string]any)["Host"] != "cdn.example.com" {
		t.Errorf("vmess ws wrong: %v", tr)
	}
	if tls := nodes[1].Out["tls"].(map[string]any); tls["server_name"] != "cdn.example.com" || tls["insecure"] != true {
		t.Errorf("vmess tls wrong: %v", tls)
	}
	if nodes[2].Name != "🇸🇬 SG trojan" {
		t.Errorf("escaped emoji name not decoded: %q", nodes[2].Name)
	}
	if a := nodes[2].Out["tls"].(map[string]any)["alpn"].([]any); len(a) != 2 || a[1] != "http/1.1" {
		t.Errorf("alpn wrong: %v", a)
	}
	r := nodes[3].Out["tls"].(map[string]any)["reality"].(map[string]any)
	if r["public_key"] != "PUBKEY" || r["short_id"] != "abcd" || nodes[3].Out["flow"] != "xtls-rprx-vision" {
		t.Errorf("reality wrong: %v", nodes[3].Out)
	}
	if nodes[4].Type != "hy2" || nodes[4].Out["obfs"].(map[string]any)["password"] != "ob" {
		t.Errorf("hy2 wrong: %v", nodes[4].Out)
	}
	if nodes[5].Type != "tuic" || nodes[5].Out["congestion_control"] != "bbr" {
		t.Errorf("tuic wrong: %v", nodes[5].Out)
	}
	// the location is picked up from the name, and the node survives save/load
	if g := nodes[0].Geo; g == nil || g.Place != "Hong Kong" {
		t.Errorf("geo from clash name: %v", g)
	}
	again := nodesFromLinks([]string{nodes[0].Link, nodes[1].Link})
	if len(again) != 2 || again[1].Out["transport"] == nil {
		t.Errorf("persistence round trip failed")
	}
}

func TestClashBlockStyle(t *testing.T) {
	nodes, err := parseBody(clashBlock)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 {
		t.Fatalf("want 3 nodes, got %d", len(nodes))
	}
	if nodes[0].Out["transport"].(map[string]any)["service_name"] != "svc" {
		t.Errorf("grpc wrong: %v", nodes[0].Out)
	}
	if nodes[1].Out["plugin"] != "obfs-local" || nodes[1].Out["plugin_opts"] != "obfs=tls;obfs-host=bing.com" {
		t.Errorf("obfs wrong: %v", nodes[1].Out)
	}
	if nodes[2].Out["transport"].(map[string]any)["headers"].(map[string]any)["Host"] != "nl.example.com" {
		t.Errorf("trojan ws wrong: %v", nodes[2].Out)
	}
	if _, err := parseBody("proxies:\nproxy-groups:\n  - {name: a}\n"); err == nil {
		t.Errorf("empty proxies should error")
	}
}

func TestParseDeepLink(t *testing.T) {
	ok := map[string][2]string{
		"sharkoxy://add?url=https%3A%2F%2Fa.example%2Fsub%3Ftoken%3D1&name=Work": {"https://a.example/sub?token=1", "Work"},
		"clash://install-config?url=https%3A%2F%2Fa.example%2Fx":                  {"https://a.example/x", ""},
		"sing-box://import-remote-profile?url=https%3A%2F%2Fa.example%2Fx#Home":   {"https://a.example/x", "Home"},
	}
	for in, want := range ok {
		u, n, err := parseDeepLink(in)
		if err != nil || u != want[0] || n != want[1] {
			t.Errorf("%s -> %q %q %v", in, u, n, err)
		}
	}
	for _, bad := range []string{"https://a.example", "sharkoxy://add", "sharkoxy://add?url=file%3A%2F%2F%2Fc", "sharkoxy://add?url=javascript%3Aalert(1)", "evil://add?url=https%3A%2F%2Fa"} {
		if _, _, err := parseDeepLink(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
