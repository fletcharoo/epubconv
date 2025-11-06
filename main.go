package main

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Container structure for parsing container.xml
type Container struct {
	Rootfiles struct {
		Rootfile []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfile"`
	} `xml:"rootfiles"`
}

// Package structure for parsing content.opf
type Package struct {
	Manifest struct {
		Items []struct {
			ID        string `xml:"id,attr"`
			Href      string `xml:"href,attr"`
			MediaType string `xml:"media-type,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		Itemrefs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: epub2txt <input.epub> [output.txt]")
		fmt.Println("If no output file is specified, output will be printed to stdout")
		os.Exit(1)
	}

	epubPath := os.Args[1]
	outputPath := ""
	if len(os.Args) >= 3 {
		outputPath = os.Args[2]
	}

	text, err := convertEPUBToText(epubPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error converting EPUB: %v\n", err)
		os.Exit(1)
	}

	if outputPath != "" {
		err = os.WriteFile(outputPath, []byte(text), 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error writing output file: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Successfully converted %s to %s\n", epubPath, outputPath)
	} else {
		fmt.Println(text)
	}
}

func convertEPUBToText(epubPath string) (string, error) {
	// Open the EPUB file (which is a ZIP archive)
	reader, err := zip.OpenReader(epubPath)
	if err != nil {
		return "", fmt.Errorf("failed to open EPUB file: %w", err)
	}
	defer reader.Close()

	// Find and parse container.xml to get the content.opf location
	containerPath := "META-INF/container.xml"
	var container Container
	if err := parseXMLFromZip(reader, containerPath, &container); err != nil {
		return "", fmt.Errorf("failed to parse container.xml: %w", err)
	}

	if len(container.Rootfiles.Rootfile) == 0 {
		return "", fmt.Errorf("no rootfile found in container.xml")
	}

	contentPath := container.Rootfiles.Rootfile[0].FullPath
	contentDir := filepath.Dir(contentPath)

	// Parse content.opf to get the reading order
	var pkg Package
	if err := parseXMLFromZip(reader, contentPath, &pkg); err != nil {
		return "", fmt.Errorf("failed to parse content.opf: %w", err)
	}

	// Create a map of ID to href
	idToHref := make(map[string]string)
	for _, item := range pkg.Manifest.Items {
		idToHref[item.ID] = item.Href
	}

	// Get the ordered list of content files
	var contentFiles []string
	for _, itemref := range pkg.Spine.Itemrefs {
		if href, ok := idToHref[itemref.IDRef]; ok {
			fullPath := filepath.Join(contentDir, href)
			contentFiles = append(contentFiles, fullPath)
		}
	}

	// Extract text from each content file
	var textBuilder strings.Builder
	for _, filePath := range contentFiles {
		content, err := readFileFromZip(reader, filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to read %s: %v\n", filePath, err)
			continue
		}

		text := extractTextFromHTML(content)
		if text != "" {
			textBuilder.WriteString(text)
			textBuilder.WriteString("\n\n")
		}
	}

	return textBuilder.String(), nil
}

func parseXMLFromZip(reader *zip.ReadCloser, path string, v interface{}) error {
	for _, file := range reader.File {
		if file.Name == path {
			rc, err := file.Open()
			if err != nil {
				return err
			}
			defer rc.Close()

			return xml.NewDecoder(rc).Decode(v)
		}
	}
	return fmt.Errorf("file not found in EPUB: %s", path)
}

func readFileFromZip(reader *zip.ReadCloser, path string) (string, error) {
	// Normalize path separators
	path = filepath.ToSlash(path)

	for _, file := range reader.File {
		if filepath.ToSlash(file.Name) == path {
			rc, err := file.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()

			content, err := io.ReadAll(rc)
			if err != nil {
				return "", err
			}
			return string(content), nil
		}
	}
	return "", fmt.Errorf("file not found: %s", path)
}

func extractTextFromHTML(html string) string {
	var text strings.Builder
	inTag := false
	inScript := false
	inStyle := false

	html = strings.ReplaceAll(html, "</p>", "</p>\n")
	html = strings.ReplaceAll(html, "<br>", "\n")
	html = strings.ReplaceAll(html, "<br/>", "\n")
	html = strings.ReplaceAll(html, "<br />", "\n")
	html = strings.ReplaceAll(html, "</div>", "</div>\n")
	html = strings.ReplaceAll(html, "</h1>", "</h1>\n\n")
	html = strings.ReplaceAll(html, "</h2>", "</h2>\n\n")
	html = strings.ReplaceAll(html, "</h3>", "</h3>\n\n")
	html = strings.ReplaceAll(html, "</h4>", "</h4>\n\n")

	i := 0
	for i < len(html) {
		if html[i] == '<' {
			inTag = true
			// Check for script or style tags
			if i+7 < len(html) && strings.ToLower(html[i:i+7]) == "<script" {
				inScript = true
			} else if i+9 < len(html) && strings.ToLower(html[i:i+9]) == "</script>" {
				inScript = false
				i += 9
				continue
			} else if i+6 < len(html) && strings.ToLower(html[i:i+6]) == "<style" {
				inStyle = true
			} else if i+8 < len(html) && strings.ToLower(html[i:i+8]) == "</style>" {
				inStyle = false
				i += 8
				continue
			}
		} else if html[i] == '>' {
			inTag = false
			i++
			continue
		}

		if !inTag && !inScript && !inStyle {
			text.WriteByte(html[i])
		}
		i++
	}

	// Clean up the text
	result := text.String()
	result = strings.ReplaceAll(result, "&nbsp;", " ")
	result = strings.ReplaceAll(result, "&amp;", "&")
	result = strings.ReplaceAll(result, "&lt;", "<")
	result = strings.ReplaceAll(result, "&gt;", ">")
	result = strings.ReplaceAll(result, "&quot;", "\"")
	result = strings.ReplaceAll(result, "&#39;", "'")

	// Remove excessive whitespace
	lines := strings.Split(result, "\n")
	var cleanedLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleanedLines = append(cleanedLines, line)
		}
	}

	return strings.Join(cleanedLines, "\n")
}
