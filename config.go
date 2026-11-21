package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Plain HTTP: one fewer handshake than HTTPS, so the number is close to the real round trip.
const defaultTestURL = "http://www.gstatic.com/generate_204"
const oldTestURL = "https://www.gstatic.com/generate_204"

// Domains sent straight out (no proxy) in smart mode.
var bypassSuffixes = []string{
	"cn", "local", "localhost",
	"baidu.com", "bdstatic.com", "qq.com", "weixin.com", "wechat.com", "tencent.com", "gtimg.com", "myqcloud.com", "qpic.cn",
	"taobao.com", "tmall.com", "alipay.com", "alicdn.com", "aliyun.com", "aliyuncs.com", "1688.com", "alibaba.com", "dingtalk.com",
	"163.com", "126.net", "netease.com", "weibo.com", "sinaimg.com", "zhihu.com", "zhimg.com", "bilibili.com", "hdslb.com", "bilivideo.com",
	"douyin.com", "douyinpic.com", "douyincdn.com", "bytedance.com", "toutiao.com", "byteimg.com", "jd.com", "360buyimg.com",
	"xiaohongshu.com", "meituan.com", "dianping.com", "mi.com", "xiaomi.com", "huawei.com", "hicloud.com", "sogou.com",
	"csdn.net", "cnblogs.com", "iqiyi.com", "youku.com", "qiyi.com", "kuaishou.com", "pinduoduo.com", "ctrip.com", "didiglobal.com",
	"tsinghua.edu", "edu.cn", "gov.cn", "360.com", "so.com", "ximalaya.com", "douban.com", "xunlei.com", "cctv.com", "people.com",
	"mmstat.com", "gtimg.com", "sf-express.com", "12306.com", "chinaz.com", "microsoftonline.cn", "windows.net.cn",
}

// cleanDomains turns a free-form list (one per line, or comma separated) into domain suffixes.
func cleanDomains(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' || r == ';' || r == ' ' || r == '\t' }) {
		d := strings.ToLower(strings.TrimSpace(f))
		d = strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
		if i := strings.IndexAny(d, "/?#:"); i >= 0 {
			d = d[:i]
		}
		d = strings.TrimPrefix(strings.TrimPrefix(d, "*."), ".")
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// versionAtLeast parses "1.12.3" style strings. Unknown versions count as new.
func versionAtLeast(v string, major, minor int) bool {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return true
	}
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return true
	}
	ma, e1 := strconv.Atoi(parts[0])
	digits := strings.TrimRightFunc(parts[1], func(r rune) bool { return r < '0' || r > '9' })
	mi, e2 := strconv.Atoi(digits)
	if e1 != nil || e2 != nil {
		return true
	}
	return ma > major || (ma == major && mi >= minor)
}

func buildConfig(nodes []Node, set Settings, mixed, api int, secret string, coreVer string) ([]byte, error) {
	if len(nodes) == 0 {
		return nil, errors.New("no nodes to configure")
	}
	var outs []any
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		o := make(map[string]any, len(n.Out)+1)
		for k, v := range n.Out {
			o[k] = v
		}
		o["tag"] = n.ID
		outs = append(outs, o)
		ids = append(ids, n.ID)
	}
	testURL := set.TestURL
	if testURL == "" {
		testURL = defaultTestURL
	}
	outs = append(outs,
		// The app does the testing and picks the node itself; the selector just switches traffic.
		// Existing connections are NOT cut on a switch (that would drop downloads and calls for a small
		// gain); the app closes them itself only when the old server is actually dead.
		map[string]any{"type": "selector", "tag": "proxy", "outbounds": ids, "default": ids[0],
			"interrupt_exist_connections": false},
		map[string]any{"type": "direct", "tag": "direct"},
	)

	rules := []any{map[string]any{"ip_is_private": true, "outbound": "direct"}}
	if d := cleanDomains(set.ProxyList); len(d) > 0 {
		rules = append(rules, map[string]any{"domain_suffix": d, "outbound": "proxy"})
	}
	if d := cleanDomains(set.DirectList); len(d) > 0 {
		rules = append(rules, map[string]any{"domain_suffix": d, "outbound": "direct"})
	}
	if set.Bypass {
		rules = append(rules, map[string]any{"domain_suffix": bypassSuffixes, "outbound": "direct"})
	}
	route := map[string]any{"rules": rules, "final": "proxy"}

	listen := "127.0.0.1"
	if set.LAN {
		listen = "0.0.0.0"
	}
	clash := map[string]any{
		"external_controller": fmt.Sprintf("127.0.0.1:%d", api),
		"secret":              secret,
	}
	if set.ClashAPI { // let browser-based Clash dashboards (yacd, MetaCubeXD...) connect
		clash["access_control_allow_origin"] = []string{"*"}
		clash["access_control_allow_private_network"] = true
	}
	cfg := map[string]any{
		"log": map[string]any{"level": "warn", "timestamp": true},
		"inbounds": []any{map[string]any{
			"type": "mixed", "tag": "mixed-in", "listen": listen, "listen_port": mixed,
		}},
		"outbounds":    outs,
		"route":        route,
		"experimental": map[string]any{"clash_api": clash},
	}
	// sing-box 1.12+ wants an explicit resolver for server domain names.
	if versionAtLeast(coreVer, 1, 12) {
		cfg["dns"] = map[string]any{
			"servers": []any{map[string]any{"type": "local", "tag": "local-dns"}},
			"final":   "local-dns",
		}
		route["default_domain_resolver"] = "local-dns"
	}
	return json.MarshalIndent(cfg, "", "  ")
}
