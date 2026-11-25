package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// parsePlist decodes an XML property list into map[string]any, []any, string,
// int64, float64, bool and time.Time. Data values are returned as raw base64.
func parsePlist(b []byte) (map[string]any, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	for {
		t, err := d.Token()
		if err != nil {
			return nil, fmt.Errorf("plist: %w", err)
		}
		s, ok := t.(xml.StartElement)
		if !ok || s.Name.Local == "plist" {
			continue
		}
		v, err := plistValue(d, s)
		if err != nil {
			return nil, fmt.Errorf("plist: %w", err)
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("plist: top level is <%s>, not <dict>", s.Name.Local)
		}
		return m, nil
	}
}

func plistValue(d *xml.Decoder, s xml.StartElement) (any, error) {
	switch s.Name.Local {
	case "dict":
		m := map[string]any{}
		key := ""
		for {
			c, err := nextChild(d)
			if err != nil || c == nil {
				return m, err
			}
			if c.Name.Local == "key" {
				if err := d.DecodeElement(&key, c); err != nil {
					return nil, err
				}
				continue
			}
			if m[key], err = plistValue(d, *c); err != nil {
				return nil, err
			}
		}
	case "array":
		a := []any{}
		for {
			c, err := nextChild(d)
			if err != nil || c == nil {
				return a, err
			}
			v, err := plistValue(d, *c)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
	}
	var txt string
	if err := d.DecodeElement(&txt, &s); err != nil {
		return nil, err
	}
	switch s.Name.Local {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "integer":
		return strconv.ParseInt(strings.TrimSpace(txt), 10, 64)
	case "real":
		return strconv.ParseFloat(strings.TrimSpace(txt), 64)
	case "date":
		return time.Parse(time.RFC3339, strings.TrimSpace(txt))
	}
	return txt, nil
}

// nextChild returns the next start element, or nil at the end of the parent.
func nextChild(d *xml.Decoder) (*xml.StartElement, error) {
	for {
		t, err := d.Token()
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		if err != nil {
			return nil, err
		}
		switch e := t.(type) {
		case xml.StartElement:
			return &e, nil
		case xml.EndElement:
			return nil, nil
		}
	}
}
