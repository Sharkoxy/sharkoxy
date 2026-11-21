package main

import (
	"errors"
	"strconv"
	"strings"
)

// A deliberately small YAML reader: enough for the `proxies:` section of Clash profiles
// (block maps/lists, inline {flow} maps and [flow] lists, quoted strings, comments).
// Every scalar comes back as a string.

type yline struct {
	indent int
	text   string
}

func stripComment(s string) string {
	inS, inD := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inD:
			inS = !inS
		case c == '"' && !inS && (i == 0 || s[i-1] != '\\'):
			inD = !inD
		case c == '#' && !inS && !inD && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t'):
			return strings.TrimRight(s[:i], " \t\r")
		}
	}
	return strings.TrimRight(s, " \t\r")
}

func yamlLines(src string) []yline {
	var out []yline
	src = strings.ReplaceAll(strings.ReplaceAll(src, "\r\n", "\n"), "\t", "  ")
	for _, raw := range strings.Split(src, "\n") {
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		ind := 0
		for ind < len(line) && line[ind] == ' ' {
			ind++
		}
		out = append(out, yline{ind, line[ind:]})
	}
	return out
}

func isSeqItem(t string) bool { return t == "-" || strings.HasPrefix(t, "- ") }

// splitKey splits "key: value" (colon followed by space or end of line, outside quotes).
func splitKey(s string) (key, val string, ok bool) {
	inS, inD := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inD:
			inS = !inS
		case c == '"' && !inS && (i == 0 || s[i-1] != '\\'):
			inD = !inD
		case c == ':' && !inS && !inD && (i+1 == len(s) || s[i+1] == ' '):
			k := strings.TrimSpace(s[:i])
			if len(k) >= 2 && (k[0] == '"' || k[0] == '\'') && k[len(k)-1] == k[0] {
				k = k[1 : len(k)-1]
			}
			return k, strings.TrimSpace(s[i+1:]), true
		}
	}
	return "", "", false
}

type yparser struct {
	lines []yline
	pos   int
}

func (p *yparser) parseNode() (any, error) {
	if p.pos >= len(p.lines) {
		return "", nil
	}
	l := p.lines[p.pos]
	if isSeqItem(l.text) {
		return p.parseSeq(l.indent)
	}
	return p.parseMap(l.indent)
}

func (p *yparser) parseSeq(indent int) (any, error) {
	var out []any
	for p.pos < len(p.lines) {
		l := p.lines[p.pos]
		if l.indent != indent || !isSeqItem(l.text) {
			break
		}
		rest := strings.TrimLeft(strings.TrimPrefix(l.text, "-"), " ")
		switch {
		case rest == "":
			p.pos++
			if p.pos < len(p.lines) && p.lines[p.pos].indent > indent {
				v, err := p.parseNode()
				if err != nil {
					return nil, err
				}
				out = append(out, v)
			} else {
				out = append(out, "")
			}
		case rest[0] != '{' && rest[0] != '[':
			if _, _, isKV := splitKey(rest); isKV {
				// "- key: value" starts a block map whose keys line up after the dash
				off := len(l.text) - len(rest)
				p.lines[p.pos] = yline{indent + off, rest}
				v, err := p.parseMap(indent + off)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
				continue
			}
			fallthrough
		default:
			p.pos++
			v, err := parseInline(rest)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
	}
	return out, nil
}

func (p *yparser) parseMap(indent int) (any, error) {
	m := map[string]any{}
	for p.pos < len(p.lines) {
		l := p.lines[p.pos]
		if l.indent != indent || isSeqItem(l.text) {
			break
		}
		k, v, ok := splitKey(l.text)
		if !ok {
			return nil, errors.New("yaml: expected 'key: value' near " + strconv.Quote(l.text))
		}
		p.pos++
		if v == "|" || v == ">" || v == "|-" || v == ">-" {
			var parts []string
			for p.pos < len(p.lines) && p.lines[p.pos].indent > indent {
				parts = append(parts, p.lines[p.pos].text)
				p.pos++
			}
			m[k] = strings.Join(parts, "\n")
			continue
		}
		if v == "" {
			if p.pos < len(p.lines) {
				nl := p.lines[p.pos]
				if nl.indent > indent {
					child, err := p.parseNode()
					if err != nil {
						return nil, err
					}
					m[k] = child
					continue
				}
				if nl.indent == indent && isSeqItem(nl.text) {
					child, err := p.parseSeq(indent)
					if err != nil {
						return nil, err
					}
					m[k] = child
					continue
				}
			}
			m[k] = ""
			continue
		}
		val, err := parseInline(v)
		if err != nil {
			return nil, err
		}
		m[k] = val
	}
	return m, nil
}

func plainScalar(s string) string {
	s = strings.TrimSpace(s)
	if s == "~" || s == "null" || s == "Null" || s == "NULL" {
		return ""
	}
	return s
}

// parseInline handles one value: a {flow map}, a [flow list], a quoted string, or plain text.
func parseInline(s string) (any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	switch s[0] {
	case '{', '[', '"', '\'':
		f := &fparser{s: s}
		return f.value()
	}
	return plainScalar(s), nil
}

type fparser struct {
	s string
	i int
}

func (f *fparser) ws() {
	for f.i < len(f.s) && (f.s[f.i] == ' ' || f.s[f.i] == '\t') {
		f.i++
	}
}

func (f *fparser) quoted() (string, error) {
	q := f.s[f.i]
	st := f.i
	f.i++
	for f.i < len(f.s) {
		c := f.s[f.i]
		if q == '"' && c == '\\' {
			f.i += 2
			continue
		}
		if c == q {
			if q == '\'' && f.i+1 < len(f.s) && f.s[f.i+1] == '\'' {
				f.i += 2
				continue
			}
			f.i++
			raw := f.s[st:f.i]
			if q == '"' {
				if u, err := strconv.Unquote(raw); err == nil {
					return u, nil
				}
				return raw[1 : len(raw)-1], nil
			}
			return strings.ReplaceAll(raw[1:len(raw)-1], "''", "'"), nil
		}
		f.i++
	}
	return "", errors.New("yaml: unterminated quote")
}

func (f *fparser) value() (any, error) {
	f.ws()
	if f.i >= len(f.s) {
		return "", nil
	}
	switch f.s[f.i] {
	case '{':
		return f.mapping()
	case '[':
		return f.seq()
	case '"', '\'':
		return f.quoted()
	}
	st := f.i
	for f.i < len(f.s) && f.s[f.i] != ',' && f.s[f.i] != '}' && f.s[f.i] != ']' {
		f.i++
	}
	return plainScalar(f.s[st:f.i]), nil
}

func (f *fparser) seq() (any, error) {
	f.i++ // [
	var out []any
	for {
		f.ws()
		if f.i >= len(f.s) {
			return nil, errors.New("yaml: unterminated [")
		}
		if f.s[f.i] == ']' {
			f.i++
			return out, nil
		}
		v, err := f.value()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		f.ws()
		if f.i < len(f.s) && f.s[f.i] == ',' {
			f.i++
		}
	}
}

func (f *fparser) mapping() (any, error) {
	f.i++ // {
	m := map[string]any{}
	for {
		f.ws()
		if f.i >= len(f.s) {
			return nil, errors.New("yaml: unterminated {")
		}
		if f.s[f.i] == '}' {
			f.i++
			return m, nil
		}
		var key string
		if f.s[f.i] == '"' || f.s[f.i] == '\'' {
			k, err := f.quoted()
			if err != nil {
				return nil, err
			}
			key = k
		} else {
			st := f.i
			for f.i < len(f.s) && f.s[f.i] != ':' && f.s[f.i] != ',' && f.s[f.i] != '}' {
				f.i++
			}
			key = strings.TrimSpace(f.s[st:f.i])
		}
		f.ws()
		if f.i < len(f.s) && f.s[f.i] == ':' {
			f.i++
			v, err := f.value()
			if err != nil {
				return nil, err
			}
			m[key] = v
		} else {
			m[key] = ""
		}
		f.ws()
		if f.i < len(f.s) && f.s[f.i] == ',' {
			f.i++
		}
	}
}

// clashProxies returns the entries of a Clash profile's top-level `proxies:` list.
func clashProxies(text string) ([]map[string]any, error) {
	lines := yamlLines(text)
	start := -1
	for i, l := range lines {
		if l.indent == 0 && (strings.HasPrefix(l.text, "proxies:") || strings.HasPrefix(l.text, "Proxy:")) {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, errors.New("no 'proxies:' section found")
	}
	var sub []yline
	for _, l := range lines[start+1:] {
		if l.indent == 0 && !isSeqItem(l.text) {
			break
		}
		sub = append(sub, l)
	}
	if len(sub) == 0 {
		return nil, errors.New("the 'proxies:' section is empty (proxy-providers aren't supported)")
	}
	p := &yparser{lines: sub}
	root, err := p.parseNode()
	if err != nil {
		return nil, err
	}
	items, ok := root.([]any)
	if !ok {
		return nil, errors.New("'proxies:' is not a list")
	}
	var out []map[string]any
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func looksLikeClash(s string) bool {
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimRight(l, " \r")
		if l == "proxies:" || strings.HasPrefix(l, "proxies:") || l == "Proxy:" {
			return true
		}
	}
	return false
}
