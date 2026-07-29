package exporter

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrInvalidExportJSON = errors.New("dados JSON da exportação são inválidos")

// BuildLGPDArchive creates the single immutable artifact uploaded by the
// worker. It contains both the machine-readable JSON and a human-readable PDF.
func BuildLGPDArchive(
	patientName string,
	requestedAt time.Time,
	rawJSON []byte,
) ([]byte, error) {
	if !json.Valid(rawJSON) {
		return nil, ErrInvalidExportJSON
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, rawJSON, "", "  "); err != nil {
		return nil, err
	}
	jsonDocument := append(indented.Bytes(), '\n')
	pdfDocument, err := buildPDF(patientName, requestedAt, jsonDocument)
	if err != nil {
		return nil, err
	}

	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	if err := writeArchiveFile(writer, "export.json", "application/json", requestedAt, jsonDocument); err != nil {
		return nil, err
	}
	if err := writeArchiveFile(writer, "export.pdf", "application/pdf", requestedAt, pdfDocument); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return archive.Bytes(), nil
}

func writeArchiveFile(
	writer *zip.Writer,
	name string,
	contentType string,
	modifiedAt time.Time,
	content []byte,
) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetModTime(modifiedAt.UTC())
	header.SetMode(0o600)
	header.Comment = contentType
	entry, err := writer.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = entry.Write(content)
	return err
}

func buildPDF(patientName string, requestedAt time.Time, jsonDocument []byte) ([]byte, error) {
	title := []string{
		"ACOLHE - EXPORTACAO DE DADOS PESSOAIS",
		"Titular: " + patientName,
		"Solicitada em: " + requestedAt.UTC().Format(time.RFC3339),
		"",
		"Este documento apresenta os mesmos dados do arquivo export.json.",
		"",
	}
	lines := append(title, wrapPDFText(string(jsonDocument), 96)...)
	const linesPerPage = 54
	pageCount := (len(lines) + linesPerPage - 1) / linesPerPage
	if pageCount == 0 {
		pageCount = 1
	}

	objects := make([][]byte, 0, 3+2*pageCount)
	fontObjectID := 3 + 2*pageCount
	objects = append(objects, []byte("<< /Type /Catalog /Pages 2 0 R >>"))

	kids := make([]string, 0, pageCount)
	for page := 0; page < pageCount; page++ {
		kids = append(kids, fmt.Sprintf("%d 0 R", 3+2*page))
	}
	objects = append(objects, []byte(fmt.Sprintf(
		"<< /Type /Pages /Kids [%s] /Count %d >>",
		strings.Join(kids, " "),
		pageCount,
	)))

	for page := 0; page < pageCount; page++ {
		pageObjectID := 3 + 2*page
		contentObjectID := pageObjectID + 1
		objects = append(objects, []byte(fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>",
			fontObjectID,
			contentObjectID,
		)))
		start := page * linesPerPage
		end := min(start+linesPerPage, len(lines))
		stream := pdfTextStream(lines[start:end])
		objects = append(objects, []byte(fmt.Sprintf(
			"<< /Length %d >>\nstream\n%s\nendstream",
			len(stream),
			stream,
		)))
	}
	objects = append(objects, []byte("<< /Type /Font /Subtype /Type1 /BaseFont /Courier >>"))

	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		objectID := index + 1
		offsets[objectID] = pdf.Len()
		fmt.Fprintf(&pdf, "%d 0 obj\n", objectID)
		pdf.Write(object)
		pdf.WriteString("\nendobj\n")
	}
	xrefOffset := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n", len(objects)+1)
	pdf.WriteString("0000000000 65535 f \n")
	for objectID := 1; objectID <= len(objects); objectID++ {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offsets[objectID])
	}
	fmt.Fprintf(
		&pdf,
		"trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1,
		xrefOffset,
	)
	return pdf.Bytes(), nil
}

func pdfTextStream(lines []string) string {
	var stream strings.Builder
	stream.WriteString("BT\n/F1 7 Tf\n36 806 Td\n10 TL\n")
	for _, line := range lines {
		stream.WriteByte('(')
		stream.WriteString(escapePDFString(asciiPDF(line)))
		stream.WriteString(") Tj\nT*\n")
	}
	stream.WriteString("ET")
	return stream.String()
}

func wrapPDFText(text string, width int) []string {
	sourceLines := strings.Split(text, "\n")
	lines := make([]string, 0, len(sourceLines))
	for _, source := range sourceLines {
		if source == "" {
			lines = append(lines, "")
			continue
		}
		remaining := source
		for utf8.RuneCountInString(remaining) > width {
			runes := []rune(remaining)
			cut := width
			for cut > width/2 && runes[cut] != ' ' {
				cut--
			}
			if cut <= width/2 {
				cut = width
			}
			lines = append(lines, string(runes[:cut]))
			remaining = strings.TrimLeft(string(runes[cut:]), " ")
		}
		lines = append(lines, remaining)
	}
	return lines
}

func escapePDFString(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"(", "\\(",
		")", "\\)",
		"\r", "",
		"\t", "  ",
	)
	return replacer.Replace(value)
}

func asciiPDF(value string) string {
	replacer := strings.NewReplacer(
		"á", "a", "à", "a", "ã", "a", "â", "a", "ä", "a",
		"Á", "A", "À", "A", "Ã", "A", "Â", "A", "Ä", "A",
		"é", "e", "è", "e", "ê", "e", "ë", "e",
		"É", "E", "È", "E", "Ê", "E", "Ë", "E",
		"í", "i", "ì", "i", "î", "i", "ï", "i",
		"Í", "I", "Ì", "I", "Î", "I", "Ï", "I",
		"ó", "o", "ò", "o", "õ", "o", "ô", "o", "ö", "o",
		"Ó", "O", "Ò", "O", "Õ", "O", "Ô", "O", "Ö", "O",
		"ú", "u", "ù", "u", "û", "u", "ü", "u",
		"Ú", "U", "Ù", "U", "Û", "U", "Ü", "U",
		"ç", "c", "Ç", "C", "–", "-", "—", "-", "“", "\"", "”", "\"",
	)
	value = replacer.Replace(value)
	var output strings.Builder
	for _, char := range value {
		if char >= 32 && char <= 126 {
			output.WriteRune(char)
		} else {
			output.WriteString("\\u")
			output.WriteString(strconv.FormatInt(int64(char), 16))
		}
	}
	return output.String()
}
