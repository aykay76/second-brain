package onedrive

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// ExtractText returns plain text from file content based on the file extension.
// Supported: .md, .txt, .docx, .pdf (basic).
func ExtractText(name string, data []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".md", ".txt":
		return string(data), nil
	case ".docx":
		return extractDocx(data)
	case ".pdf":
		return extractPDFText(data)
	default:
		return "", fmt.Errorf("unsupported file type: %s", ext)
	}
}

// extractDocx reads text from a .docx file's word/document.xml.
func extractDocx(data []byte) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open docx zip: %w", err)
	}

	for _, f := range r.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("open document.xml: %w", err)
		}
		defer rc.Close()
		return extractWordXML(rc)
	}

	return "", fmt.Errorf("word/document.xml not found in docx")
}

// extractWordXML parses the Office Open XML and extracts paragraph text.
func extractWordXML(r io.Reader) (string, error) {
	decoder := xml.NewDecoder(r)
	var paragraphs []string
	var currentPara strings.Builder
	inText := false

	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse document.xml: %w", err)
		}

		inText, paragraphs = processWordToken(tok, inText, &currentPara, paragraphs)
	}

	if text := strings.TrimSpace(currentPara.String()); text != "" {
		paragraphs = append(paragraphs, text)
	}

	return strings.Join(paragraphs, "\n"), nil
}

func processWordToken(tok xml.Token, inText bool, para *strings.Builder, paragraphs []string) (bool, []string) {
	switch t := tok.(type) {
	case xml.StartElement:
		if t.Name.Local == "t" {
			return true, paragraphs
		}
	case xml.EndElement:
		if t.Name.Local == "t" {
			return false, paragraphs
		}
		if t.Name.Local == "p" {
			if text := strings.TrimSpace(para.String()); text != "" {
				paragraphs = append(paragraphs, text)
			}
			para.Reset()
		}
	case xml.CharData:
		if inText {
			para.Write(t)
		}
	}
	return inText, paragraphs
}

// extractPDFText does basic text extraction from PDF files by looking for
// text stream content between BT/ET markers. This handles simple PDFs
// but not all encoding schemes. For full PDF support, a dedicated library
// would be needed.
func extractPDFText(data []byte) (string, error) {
	content := string(data)
	var result strings.Builder

	// Look for text objects between BT (begin text) and ET (end text)
	for {
		btIdx := strings.Index(content, "BT")
		if btIdx == -1 {
			break
		}
		content = content[btIdx+2:]

		etIdx := strings.Index(content, "ET")
		if etIdx == -1 {
			break
		}

		textBlock := content[:etIdx]
		content = content[etIdx+2:]

		extractPDFTextOperators(&result, textBlock)
	}

	text := result.String()
	if text == "" {
		return "", fmt.Errorf("no extractable text found in PDF (may require OCR or advanced PDF parsing)")
	}

	return strings.TrimSpace(text), nil
}

// extractPDFTextOperators extracts text from Tj and TJ operators in a PDF text object.
func extractPDFTextOperators(w *strings.Builder, block string) {
	lines := strings.Split(block, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		processPDFLine(w, line)
	}
}

func processPDFLine(w *strings.Builder, line string) {
	switch {
	case strings.HasSuffix(line, "Tj"):
		extractParenText(w, line, "")
	case strings.HasSuffix(line, "'"):
		extractParenText(w, line, "\n")
	case strings.HasSuffix(line, "TJ"):
		extractTJArray(w, line)
	case strings.HasSuffix(line, "Td"), strings.HasSuffix(line, "TD"):
		w.WriteByte('\n')
	}
}

func extractParenText(w *strings.Builder, line, prefix string) {
	start := strings.Index(line, "(")
	if start == -1 {
		return
	}
	end := strings.LastIndex(line, ")")
	if end <= start {
		return
	}
	w.WriteString(prefix)
	w.WriteString(unescapePDFString(line[start+1 : end]))
}

func extractTJArray(w *strings.Builder, line string) {
	bracketStart := strings.Index(line, "[")
	bracketEnd := strings.LastIndex(line, "]")
	if bracketStart == -1 || bracketEnd == -1 {
		return
	}
	arr := line[bracketStart+1 : bracketEnd]

	inParen := false
	var text strings.Builder
	for i := 0; i < len(arr); i++ {
		if arr[i] == '(' && !inParen {
			inParen = true
			continue
		}
		if arr[i] == ')' && inParen {
			inParen = false
			w.WriteString(unescapePDFString(text.String()))
			text.Reset()
			continue
		}
		if inParen {
			if arr[i] == '\\' && i+1 < len(arr) {
				i++
				text.WriteByte(arr[i])
				continue
			}
			text.WriteByte(arr[i])
		}
	}
}

func unescapePDFString(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '(':
				b.WriteByte('(')
			case ')':
				b.WriteByte(')')
			case '\\':
				b.WriteByte('\\')
			default:
				b.WriteByte(s[i])
			}
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// IsSupportedExtension checks if a filename has a supported extension.
func IsSupportedExtension(name string, extensions []string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, e := range extensions {
		if strings.ToLower(e) == ext {
			return true
		}
	}
	return false
}
