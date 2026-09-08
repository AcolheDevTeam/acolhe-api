package exporter_test

import (
	"archive/zip"
	"bytes"
	"io"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joycesilva/acolhe-api/internal/exporter"
)

func TestBuildLGPDArchiveContainsReadableJSONAndPDF(t *testing.T) {
	requestedAt := time.Date(2026, time.July, 29, 9, 30, 0, 0, time.UTC)
	archive, err := exporter.BuildLGPDArchive(
		"Paciente Árvore",
		requestedAt,
		[]byte(`{"patient":{"fullName":"Paciente Árvore"},"consents":[]}`),
	)
	require.NoError(t, err)

	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	require.NoError(t, err)
	require.Len(t, reader.File, 2)
	files := map[string][]byte{}
	for _, file := range reader.File {
		entry, openErr := file.Open()
		require.NoError(t, openErr)
		content, readErr := io.ReadAll(entry)
		require.NoError(t, readErr)
		require.NoError(t, entry.Close())
		files[file.Name] = content
		assert.Equal(t, fs.FileMode(0o600), file.Mode().Perm())
	}
	assert.JSONEq(t,
		`{"patient":{"fullName":"Paciente Árvore"},"consents":[]}`,
		string(files["export.json"]),
	)
	assert.True(t, strings.HasPrefix(string(files["export.pdf"]), "%PDF-1.4"))
	assert.Contains(t, string(files["export.pdf"]), "Paciente Arvore")
	assert.True(t, strings.HasSuffix(string(files["export.pdf"]), "%%EOF\n"))
}

func TestBuildLGPDArchiveRejectsInvalidJSON(t *testing.T) {
	_, err := exporter.BuildLGPDArchive("Paciente", time.Now(), []byte(`{`))
	assert.ErrorIs(t, err, exporter.ErrInvalidExportJSON)
}
