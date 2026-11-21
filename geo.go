package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Geo is where a node is on the world map.
type Geo struct {
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
	Place string  `json:"place"`
}

type geoEntry struct {
	place    string
	lat, lon float64
	code     string // ISO country code, used for flag emoji
	keys     []string
}

// Most subscriptions name nodes after their location, so we read it from the name
// (English, Chinese, country codes, flag emoji). Unknown ones fall back to IP lookup.
var geoTable = []geoEntry{
	{"Hong Kong", 22.32, 114.17, "hk", []string{"hk", "hong kong", "hongkong", "香港"}},
	{"Taipei", 25.03, 121.56, "tw", []string{"tw", "taiwan", "taipei", "台湾", "台北", "新北", "台灣"}},
	{"Tokyo", 35.68, 139.69, "jp", []string{"jp", "japan", "tokyo", "日本", "东京", "東京"}},
	{"Osaka", 34.69, 135.5, "", []string{"osaka", "大阪"}},
	{"Singapore", 1.35, 103.82, "sg", []string{"sg", "singapore", "新加坡", "狮城", "獅城"}},
	{"Seoul", 37.57, 126.98, "kr", []string{"kr", "korea", "seoul", "韩国", "韓國", "首尔", "首爾"}},
	{"Macau", 22.2, 113.54, "mo", []string{"macau", "macao", "澳门", "澳門"}},
	{"United States", 39.5, -98.35, "us", []string{"us", "usa", "united states", "america", "美国", "美國"}},
	{"Los Angeles", 34.05, -118.24, "", []string{"los angeles", "losangeles", "lax", "洛杉矶", "洛杉磯"}},
	{"San Jose", 37.34, -121.89, "", []string{"san jose", "sanjose", "silicon valley", "sfo", "san francisco", "圣何塞", "硅谷", "旧金山"}},
	{"Seattle", 47.61, -122.33, "", []string{"seattle", "西雅图"}},
	{"New York", 40.71, -74.0, "", []string{"new york", "newyork", "nyc", "纽约", "紐約"}},
	{"Chicago", 41.88, -87.63, "", []string{"chicago", "芝加哥"}},
	{"Dallas", 32.78, -96.8, "", []string{"dallas", "达拉斯"}},
	{"Miami", 25.76, -80.19, "", []string{"miami", "迈阿密"}},
	{"Ashburn", 39.04, -77.49, "", []string{"ashburn", "virginia", "阿什本"}},
	{"Toronto", 43.65, -79.38, "", []string{"toronto", "多伦多"}},
	{"Vancouver", 49.28, -123.12, "", []string{"vancouver", "温哥华"}},
	{"Canada", 56.13, -106.35, "ca", []string{"ca", "canada", "加拿大"}},
	{"London", 51.51, -0.13, "gb", []string{"uk", "gb", "united kingdom", "england", "britain", "london", "英国", "英國", "伦敦", "倫敦"}},
	{"Frankfurt", 50.11, 8.68, "de", []string{"de", "germany", "frankfurt", "德国", "德國", "法兰克福", "法蘭克福"}},
	{"Paris", 48.86, 2.35, "fr", []string{"fr", "france", "paris", "法国", "法國", "巴黎"}},
	{"Amsterdam", 52.37, 4.9, "nl", []string{"nl", "netherlands", "holland", "amsterdam", "荷兰", "荷蘭", "阿姆斯特丹"}},
	{"Moscow", 55.76, 37.62, "ru", []string{"ru", "russia", "moscow", "俄罗斯", "俄羅斯", "莫斯科"}},
	{"Mumbai", 19.08, 72.88, "in", []string{"india", "mumbai", "印度", "孟买"}},
	{"Sydney", -33.87, 151.21, "au", []string{"au", "australia", "sydney", "澳大利亚", "澳洲", "悉尼"}},
	{"Auckland", -36.85, 174.76, "nz", []string{"nz", "new zealand", "auckland", "新西兰"}},
	{"Istanbul", 41.01, 28.98, "tr", []string{"tr", "turkey", "turkiye", "istanbul", "土耳其"}},
	{"São Paulo", -23.55, -46.63, "br", []string{"br", "brazil", "sao paulo", "saopaulo", "巴西", "圣保罗"}},
	{"Buenos Aires", -34.6, -58.38, "ar", []string{"ar", "argentina", "阿根廷"}},
	{"Santiago", -33.45, -70.67, "cl", []string{"chile", "智利"}},
	{"Lima", -12.05, -77.04, "pe", []string{"peru", "秘鲁"}},
	{"Bogotá", 4.71, -74.07, "co", []string{"colombia", "哥伦比亚"}},
	{"Mexico City", 19.43, -99.13, "mx", []string{"mx", "mexico", "墨西哥"}},
	{"Bangkok", 13.76, 100.5, "th", []string{"th", "thailand", "bangkok", "泰国", "泰國", "曼谷"}},
	{"Hanoi", 21.03, 105.85, "vn", []string{"vn", "vietnam", "hanoi", "越南"}},
	{"Kuala Lumpur", 3.14, 101.69, "my", []string{"my", "malaysia", "kuala lumpur", "马来西亚", "馬來西亞", "吉隆坡"}},
	{"Jakarta", -6.21, 106.85, "id", []string{"indonesia", "jakarta", "印尼", "印度尼西亚"}},
	{"Manila", 14.6, 120.98, "ph", []string{"ph", "philippines", "manila", "菲律宾", "菲律賓"}},
	{"Phnom Penh", 11.56, 104.92, "kh", []string{"kh", "cambodia", "柬埔寨"}},
	{"Dhaka", 23.81, 90.41, "bd", []string{"bd", "bangladesh", "孟加拉"}},
	{"Karachi", 24.86, 67.01, "pk", []string{"pk", "pakistan", "巴基斯坦"}},
	{"Almaty", 43.24, 76.89, "kz", []string{"kz", "kazakhstan", "哈萨克斯坦"}},
	{"Ulaanbaatar", 47.89, 106.91, "mn", []string{"mongolia", "蒙古"}},
	{"Dubai", 25.2, 55.27, "ae", []string{"ae", "uae", "dubai", "阿联酋", "迪拜"}},
	{"Riyadh", 24.71, 46.68, "sa", []string{"saudi", "riyadh", "沙特"}},
	{"Tel Aviv", 32.09, 34.78, "il", []string{"il", "israel", "tel aviv", "以色列"}},
	{"Madrid", 40.42, -3.7, "es", []string{"es", "spain", "madrid", "西班牙"}},
	{"Lisbon", 38.72, -9.14, "pt", []string{"pt", "portugal", "lisbon", "葡萄牙"}},
	{"Milan", 45.46, 9.19, "it", []string{"italy", "milan", "意大利"}},
	{"Zürich", 47.38, 8.54, "ch", []string{"ch", "switzerland", "zurich", "瑞士"}},
	{"Vienna", 48.21, 16.37, "at", []string{"austria", "vienna", "奥地利"}},
	{"Brussels", 50.85, 4.35, "be", []string{"be", "belgium", "brussels", "比利时"}},
	{"Luxembourg", 49.61, 6.13, "lu", []string{"luxembourg", "卢森堡"}},
	{"Dublin", 53.35, -6.26, "ie", []string{"ie", "ireland", "dublin", "爱尔兰"}},
	{"Stockholm", 59.33, 18.07, "se", []string{"se", "sweden", "stockholm", "瑞典"}},
	{"Oslo", 59.91, 10.75, "no", []string{"norway", "oslo", "挪威"}},
	{"Copenhagen", 55.68, 12.57, "dk", []string{"dk", "denmark", "copenhagen", "丹麦"}},
	{"Helsinki", 60.17, 24.94, "fi", []string{"fi", "finland", "helsinki", "芬兰"}},
	{"Reykjavík", 64.15, -21.94, "is", []string{"iceland", "冰岛"}},
	{"Warsaw", 52.23, 21.01, "pl", []string{"pl", "poland", "warsaw", "波兰"}},
	{"Prague", 50.08, 14.44, "cz", []string{"cz", "czech", "prague", "捷克"}},
	{"Budapest", 47.5, 19.04, "hu", []string{"hu", "hungary", "budapest", "匈牙利"}},
	{"Bucharest", 44.43, 26.1, "ro", []string{"ro", "romania", "bucharest", "罗马尼亚"}},
	{"Sofia", 42.7, 23.32, "bg", []string{"bg", "bulgaria", "sofia", "保加利亚"}},
	{"Athens", 37.98, 23.73, "gr", []string{"gr", "greece", "athens", "希腊"}},
	{"Kyiv", 50.45, 30.52, "ua", []string{"ua", "ukraine", "kyiv", "kiev", "乌克兰"}},
	{"Vilnius", 54.69, 25.28, "lt", []string{"lt", "lithuania", "立陶宛"}},
	{"Riga", 56.95, 24.11, "lv", []string{"lv", "latvia", "拉脱维亚"}},
	{"Tallinn", 59.44, 24.75, "ee", []string{"ee", "estonia", "爱沙尼亚"}},
	{"Johannesburg", -26.2, 28.05, "za", []string{"za", "south africa", "南非"}},
	{"Cairo", 30.04, 31.24, "eg", []string{"eg", "egypt", "cairo", "埃及"}},
	{"Lagos", 6.52, 3.38, "ng", []string{"nigeria", "lagos", "尼日利亚"}},
}

func isRI(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

func geoByCode(code string) *Geo {
	for _, e := range geoTable {
		if e.code == code {
			return &Geo{Lat: e.lat, Lon: e.lon, Place: e.place}
		}
	}
	return nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// guessLocation reads a location out of a node's display name.
func guessLocation(name string) *Geo {
	rs := []rune(name)
	for i := 0; i+1 < len(rs); i++ {
		if isRI(rs[i]) && isRI(rs[i+1]) {
			code := string([]rune{rs[i] - 0x1F1E6 + 'a', rs[i+1] - 0x1F1E6 + 'a'})
			if g := geoByCode(code); g != nil {
				return g
			}
		}
	}
	// ASCII non-letters (digits, dashes, dots...) become spaces so "HK01" -> "hk"
	norm := strings.Map(func(r rune) rune {
		if r < 0x80 && !(r >= 'a' && r <= 'z') {
			return ' '
		}
		return r
	}, strings.ToLower(name))
	padded := " " + norm + " "
	best := 0
	var hit *geoEntry
	for i := range geoTable {
		e := &geoTable[i]
		for _, k := range e.keys {
			ok := false
			if isASCII(k) {
				ok = strings.Contains(padded, " "+k+" ")
			} else {
				ok = strings.Contains(norm, k)
			}
			// longest keyword wins; on a tie a city (no country code) beats a country
			if ok && (len(k) > best || (len(k) == best && hit != nil && hit.code != "" && e.code == "")) {
				best, hit = len(k), e
			}
		}
	}
	if hit == nil {
		return nil
	}
	return &Geo{Lat: hit.lat, Lon: hit.lon, Place: hit.place}
}

// ---- IP lookup fallback ------------------------------------------------------

func geoCachePath() string { return filepath.Join(dataDir, "geo.json") }

func loadGeoCache() map[string]*Geo {
	m := map[string]*Geo{}
	if b, err := os.ReadFile(geoCachePath()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// resolveGeo locates nodes whose name didn't say where they are, via their server IP.
func (a *App) resolveGeo() {
	a.mu.Lock()
	var hosts []string
	seen := map[string]bool{}
	for _, n := range a.nodes {
		if n.Geo == nil && a.geoCache[n.Server] == nil && !a.geoTried[n.Server] && !seen[n.Server] && len(hosts) < 300 {
			seen[n.Server] = true
			hosts = append(hosts, n.Server)
		}
	}
	for _, h := range hosts {
		a.geoTried[h] = true
	}
	running, port := a.running, a.mixed
	a.mu.Unlock()
	if len(hosts) == 0 {
		return
	}

	ipOf := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for _, h := range hosts {
		wg.Add(1)
		go func(h string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ip := net.ParseIP(h); ip != nil {
				mu.Lock()
				ipOf[h] = h
				mu.Unlock()
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			if ips, err := net.DefaultResolver.LookupIPAddr(ctx, h); err == nil && len(ips) > 0 {
				mu.Lock()
				ipOf[h] = ips[0].IP.String()
				mu.Unlock()
			}
		}(h)
	}
	wg.Wait()

	var ips []string
	uniq := map[string]bool{}
	for _, ip := range ipOf {
		if !uniq[ip] {
			uniq[ip] = true
			ips = append(ips, ip)
		}
	}
	if len(ips) == 0 {
		return
	}
	tr := &http.Transport{Proxy: nil}
	if running {
		if pu, err := url.Parse("http://127.0.0.1:" + itoa(port)); err == nil {
			tr.Proxy = http.ProxyURL(pu)
		}
	}
	c := &http.Client{Timeout: 15 * time.Second, Transport: tr}
	found := map[string]*Geo{}
	for i := 0; i < len(ips); i += 100 {
		end := i + 100
		if end > len(ips) {
			end = len(ips)
		}
		body, _ := json.Marshal(ips[i:end])
		resp, err := c.Post("http://ip-api.com/batch?fields=status,lat,lon,city,countryCode,query", "application/json", bytes.NewReader(body))
		if err != nil {
			continue
		}
		var rows []struct {
			Status      string  `json:"status"`
			Lat         float64 `json:"lat"`
			Lon         float64 `json:"lon"`
			City        string  `json:"city"`
			CountryCode string  `json:"countryCode"`
			Query       string  `json:"query"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&rows)
		resp.Body.Close()
		for _, r := range rows {
			if r.Status == "success" {
				found[r.Query] = &Geo{Lat: r.Lat, Lon: r.Lon, Place: first(r.City, r.CountryCode)}
			}
		}
	}
	if len(found) == 0 {
		return
	}
	a.mu.Lock()
	for h, ip := range ipOf {
		if g := found[ip]; g != nil {
			a.geoCache[h] = g
		}
	}
	b, _ := json.Marshal(a.geoCache)
	a.mu.Unlock()
	_ = os.WriteFile(geoCachePath(), b, 0o600)
}
