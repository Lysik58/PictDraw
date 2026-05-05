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

type scopeState struct {
	fill string
}

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

type canvas struct {
	MinX   float64
	MinY   float64
	Width  float64
	Height float64
}

var numRe = regexp.MustCompile(`[-+]?(?:\d*\.?\d+)`)

func ProcessSVG(input []byte) (*Result, error) {
	decoder := xml.NewDecoder(bytes.NewReader(input))
	gradients := map[string]string{}
	classFill := map[string]string{}
	var shapes []shape
	stack := []scopeState{{}}
	cv := canvas{MinX: 0, MinY: 0, Width: 210, Height: 297}

	for {
		tok, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			tag := strings.ToLower(t.Name.Local)
			attrs := attrsMap(t.Attr)
			if tag == "svg" {
				cv = canvasFromSVG(attrs)
			}

			inherited := stack[len(stack)-1].fill
			currentFill := fillFromAttrs(attrs, classFill)
			if currentFill == "" {
				currentFill = inherited
			}
			stack = append(stack, scopeState{fill: currentFill})

			switch tag {
			case "style":
				if css, err := readInlineStyle(decoder, t); err == nil {
					for k, v := range parseClassFillStyles(css) {
						classFill[k] = v
					}
				}
				continue
			case "lineargradient", "radialgradient":
				id := getAttr(t.Attr, "id")
				if id == "" {
					continue
				}
				color, err := readGradientColor(decoder, t)
				if err == nil && color != "" {
					gradients[id] = normalizeColor(color)
				}
				stack = stack[:len(stack)-1]
				continue
			case "rect", "circle", "ellipse", "path", "polygon", "polyline", "line":
				color := currentFill
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
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
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

	out := buildOutputSVG(shapes, colors, cv)
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

func buildOutputSVG(shapes []shape, colors []string, cv canvas) []byte {
	legendHeight := 58.0
	totalHeight := cv.Height + legendHeight
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="%.4f %.4f %.4f %.4f">`, cv.MinX, cv.MinY, cv.Width, totalHeight))
	b.WriteString(fmt.Sprintf(`<rect x="%.4f" y="%.4f" width="%.4f" height="%.4f" fill="white"/>`, cv.MinX, cv.MinY, cv.Width, totalHeight))
	b.WriteString(`<g>`)
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
	legendTop := cv.MinY + cv.Height + 6
	b.WriteString(fmt.Sprintf(`<line x1="%.4f" y1="%.4f" x2="%.4f" y2="%.4f" stroke="black" stroke-width="0.5"/>`, cv.MinX, legendTop, cv.MinX+cv.Width, legendTop))
	b.WriteString(fmt.Sprintf(`<text x="%.4f" y="%.4f" font-size="5" font-weight="bold">Легенда цветов</text>`, cv.MinX+2, legendTop+7))
	y := legendTop + 14
	for i, c := range colors {
		b.WriteString(fmt.Sprintf(`<rect x="%.4f" y="%.4f" width="7" height="5" fill="%s" stroke="black"/>`, cv.MinX+2, y-4, c))
		b.WriteString(fmt.Sprintf(`<text x="%.4f" y="%.1f" font-size="3" text-anchor="middle" dominant-baseline="middle">%d</text>`, cv.MinX+5.5, y-1.5, i+1))
		b.WriteString(fmt.Sprintf(`<text x="%.4f" y="%.1f" font-size="4">%d = %s</text>`, cv.MinX+12, y, i+1, c))
		y += 7
		if y > cv.MinY+totalHeight-2 {
			break
		}
	}
	b.WriteString(`</svg>`)
	return []byte(b.String())
}

func canvasFromSVG(attrs map[string]string) canvas {
	if vb := strings.TrimSpace(attrs["viewbox"]); vb != "" {
		parts := strings.Fields(strings.ReplaceAll(vb, ",", " "))
		if len(parts) == 4 {
			minX, ex1 := strconv.ParseFloat(parts[0], 64)
			minY, ex2 := strconv.ParseFloat(parts[1], 64)
			w, ex3 := strconv.ParseFloat(parts[2], 64)
			h, ex4 := strconv.ParseFloat(parts[3], 64)
			if ex1 == nil && ex2 == nil && ex3 == nil && ex4 == nil && w > 0 && h > 0 {
				return canvas{MinX: minX, MinY: minY, Width: w, Height: h}
			}
		}
	}
	w := parseLength(attrs["width"])
	h := parseLength(attrs["height"])
	if w > 0 && h > 0 {
		return canvas{MinX: 0, MinY: 0, Width: w, Height: h}
	}
	return canvas{MinX: 0, MinY: 0, Width: 210, Height: 297}
}

func parseLength(v string) float64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	n := numRe.FindString(v)
	if n == "" {
		return 0
	}
	f, err := strconv.ParseFloat(n, 64)
	if err != nil || math.IsNaN(f) || f <= 0 {
		return 0
	}
	return f
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

func fillFromAttrs(attrs map[string]string, classFill map[string]string) string {
	if v := attrs["fill"]; v != "" {
		return v
	}
	if v := styleValue(attrs["style"], "fill"); v != "" {
		return v
	}
	if cls := strings.TrimSpace(attrs["class"]); cls != "" {
		for _, c := range strings.Fields(cls) {
			if v := classFill[c]; v != "" {
				return v
			}
		}
	}
	return ""
}

func readInlineStyle(decoder *xml.Decoder, start xml.StartElement) (string, error) {
	var b strings.Builder
	for {
		tok, err := decoder.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write([]byte(t))
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return b.String(), nil
			}
		}
	}
}

func parseClassFillStyles(css string) map[string]string {
	out := map[string]string{}
	ruleRe := regexp.MustCompile(`(?s)\.([a-zA-Z0-9_-]+)\s*\{([^}]*)\}`)
	matches := ruleRe.FindAllStringSubmatch(css, -1)
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		if fill := styleValue(m[2], "fill"); fill != "" {
			out[m[1]] = fill
		}
	}
	return out
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
