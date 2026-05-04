package processor

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Result struct {
	Legend []LegendItem
	SVG    []byte
}

type LegendItem struct {
	Number int
	Color  string
}

type shape struct {
	Tag     string
	Attrs   map[string]string
	Color   string
	Number  int
	CenterX float64
	CenterY float64
}

var numRe = regexp.MustCompile(`[-+]?(?:\d*\.?\d+)`)

func ProcessSVG(input []byte) (*Result, error) {
	decoder := xml.NewDecoder(bytes.NewReader(input))
	gradients := map[string]string{}
	var shapes []shape

	for {
		tok, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		tag := strings.ToLower(start.Name.Local)
		switch tag {
		case "lineargradient", "radialgradient":
			id := getAttr(start.Attr, "id")
			if id == "" {
				continue
			}
			color, err := readGradientColor(decoder, start)
			if err == nil && color != "" {
				gradients[id] = normalizeColor(color)
			}
		case "rect", "circle", "ellipse", "path", "polygon", "polyline", "line":
			attrs := attrsMap(start.Attr)
			color := detectFill(attrs)
			if strings.HasPrefix(color, "url(#") && strings.HasSuffix(color, ")") {
				id := strings.TrimSuffix(strings.TrimPrefix(color, "url(#"), ")")
				color = gradients[id]
			}
			color = normalizeColor(color)
			if color == "" || color == "none" {
				continue
			}
			cx, cy := estimateCenter(tag, attrs)
			shapes = append(shapes, shape{Tag: tag, Attrs: attrs, Color: color, CenterX: cx, CenterY: cy})
		}
	}
	if len(shapes) == 0 {
		return nil, fmt.Errorf("no paintable shapes found")
	}

	colorNum := map[string]int{}
	var colors []string
	for _, s := range shapes {
		if _, ok := colorNum[s.Color]; !ok {
			colors = append(colors, s.Color)
		}
	}
	sort.Strings(colors)
	for i, c := range colors {
		colorNum[c] = i + 1
	}
	for i := range shapes {
		shapes[i].Number = colorNum[shapes[i].Color]
	}

	out := buildOutputSVG(shapes, colors)
	legend := make([]LegendItem, 0, len(colors))
	for i, c := range colors {
		legend = append(legend, LegendItem{Number: i + 1, Color: c})
	}
	return &Result{Legend: legend, SVG: out}, nil
}

func readGradientColor(decoder *xml.Decoder, start xml.StartElement) (string, error) {
	for {
		tok, err := decoder.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if strings.ToLower(t.Name.Local) == "stop" {
				attrs := attrsMap(t.Attr)
				c := attrs["stop-color"]
				if c == "" {
					c = styleValue(attrs["style"], "stop-color")
				}
				if c != "" {
					return c, nil
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return "", nil
			}
		}
	}
}

func buildOutputSVG(shapes []shape, colors []string) []byte {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="210mm" height="297mm" viewBox="0 0 210 297">`)
	b.WriteString(`<rect x="0" y="0" width="210" height="297" fill="white"/>`)
	b.WriteString(`<g transform="translate(10,10) scale(0.9)">`)
	for _, s := range shapes {
		b.WriteString(`<` + s.Tag)
		for k, v := range s.Attrs {
			switch k {
			case "fill", "stroke", "style":
				continue
			}
			b.WriteString(fmt.Sprintf(` %s="%s"`, k, xmlEscape(v)))
		}
		b.WriteString(` fill="white" stroke="black" stroke-width="0.3"/>`)
		b.WriteString(fmt.Sprintf(`<text x="%.2f" y="%.2f" font-size="4" text-anchor="middle" dominant-baseline="middle">%d</text>`, s.CenterX, s.CenterY, s.Number))
	}
	b.WriteString(`</g>`)
	b.WriteString(`<line x1="10" y1="245" x2="200" y2="245" stroke="black" stroke-width="0.5"/>`)
	b.WriteString(`<text x="10" y="252" font-size="5" font-weight="bold">Легенда цветов</text>`)
	y := 258.0
	for i, c := range colors {
		b.WriteString(fmt.Sprintf(`<rect x="10" y="%.1f" width="7" height="5" fill="white" stroke="black"/>`, y-4))
		b.WriteString(fmt.Sprintf(`<text x="13.5" y="%.1f" font-size="3" text-anchor="middle" dominant-baseline="middle">%d</text>`, y-1.5, i+1))
		b.WriteString(fmt.Sprintf(`<text x="20" y="%.1f" font-size="4">%d = %s</text>`, y, i+1, c))
		y += 7
		if y > 294 {
			break
		}
	}
	b.WriteString(`</svg>`)
	return []byte(b.String())
}

func estimateCenter(tag string, attrs map[string]string) (float64, float64) {
	f := func(k string) float64 { v, _ := strconv.ParseFloat(attrs[k], 64); return v }
	switch tag {
	case "rect":
		return f("x") + f("width")/2, f("y") + f("height")/2
	case "circle", "ellipse":
		return f("cx"), f("cy")
	case "line":
		return (f("x1") + f("x2")) / 2, (f("y1") + f("y2")) / 2
	case "polygon", "polyline":
		pts := parsePoints(attrs["points"])
		if len(pts) == 0 {
			return 0, 0
		}
		var sx, sy float64
		for _, p := range pts {
			sx += p[0]
			sy += p[1]
		}
		return sx / float64(len(pts)), sy / float64(len(pts))
	case "path":
		nums := numRe.FindAllString(attrs["d"], -1)
		if len(nums) < 2 {
			return 0, 0
		}
		var sx, sy float64
		count := 0
		for i := 0; i+1 < len(nums); i += 2 {
			x, _ := strconv.ParseFloat(nums[i], 64)
			y, _ := strconv.ParseFloat(nums[i+1], 64)
			sx += x
			sy += y
			count++
		}
		if count == 0 {
			return 0, 0
		}
		return sx / float64(count), sy / float64(count)
	}
	return 0, 0
}

func parsePoints(s string) [][2]float64 {
	parts := strings.Fields(strings.ReplaceAll(s, ",", " "))
	var pts [][2]float64
	for i := 0; i+1 < len(parts); i += 2 {
		x, ex := strconv.ParseFloat(parts[i], 64)
		y, ey := strconv.ParseFloat(parts[i+1], 64)
		if ex == nil && ey == nil && !math.IsNaN(x) && !math.IsNaN(y) {
			pts = append(pts, [2]float64{x, y})
		}
	}
	return pts
}

func detectFill(attrs map[string]string) string {
	if v := attrs["fill"]; v != "" {
		return v
	}
	if v := styleValue(attrs["style"], "fill"); v != "" {
		return v
	}
	return ""
}

func styleValue(style, key string) string {
	for _, p := range strings.Split(style, ";") {
		kv := strings.SplitN(strings.TrimSpace(p), ":", 2)
		if len(kv) == 2 && strings.TrimSpace(kv[0]) == key {
			return strings.TrimSpace(kv[1])
		}
	}
	return ""
}

func normalizeColor(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "rgb(") {
		nums := numRe.FindAllString(v, -1)
		if len(nums) >= 3 {
			r, _ := strconv.Atoi(nums[0])
			g, _ := strconv.Atoi(nums[1])
			b, _ := strconv.Atoi(nums[2])
			return fmt.Sprintf("#%02x%02x%02x", r, g, b)
		}
	}
	return v
}

func attrsMap(attrs []xml.Attr) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, a := range attrs {
		m[strings.ToLower(a.Name.Local)] = a.Value
	}
	return m
}

func getAttr(attrs []xml.Attr, key string) string {
	for _, a := range attrs {
		if strings.ToLower(a.Name.Local) == key {
			return a.Value
		}
	}
	return ""
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
