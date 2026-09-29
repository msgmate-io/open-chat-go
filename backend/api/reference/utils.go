package reference

// original source: github.com/MarceloPetrucio/go-scalar-api-reference
// forked for customization

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func ensureFileURL(filePath string) (string, error) {
	if strings.HasPrefix(filePath, "file://") {
		if path := strings.TrimPrefix(filePath, "file://"); !filepath.IsAbs(path) {
			currentDir, err := os.Getwd()
			if err != nil {
				return "", fmt.Errorf("error getting current directory: %w", err)
			}
			resolvedPath := filepath.Join(currentDir, path)
			return "file://" + resolvedPath, nil
		}
		return filePath, nil
	}

	if filepath.IsAbs(filePath) {
		return "file://" + filePath, nil
	}

	currentDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("error getting current directory: %w", err)
	}
	resolvedPath := filepath.Join(currentDir, filePath)
	return "file://" + resolvedPath, nil
}

func fetchContentFromURL(fileURL string) (string, error) {
	resp, err := http.Get(fileURL)
	if err != nil {
		return "", fmt.Errorf("error getting file content: %w", err)
	}
	defer resp.Body.Close()

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading file content: %w", err)
	}

	return string(content), nil
}

func readFileFromURL(fileURL string) ([]byte, error) {
	parsedURL, err := url.Parse(fileURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing URL: %w", err)
	}

	if parsedURL.Scheme != "file" {
		return nil, fmt.Errorf("unsupported URL scheme: %s", parsedURL.Scheme)
	}

	return os.ReadFile(parsedURL.Path)
}

func safeJSONConfiguration(options *Options) string {
	jsonData, _ := json.Marshal(options)
	return string(jsonData)
}

var apiReferenceTemplate = template.Must(template.New("api-reference").Parse(`<!DOCTYPE html>
    <html>
      <head>
        <title>{{.PageTitle}}</title>
        <meta charset="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <style>{{.ThemeCSS}}</style>
      </head>
      <body>
        <script id="api-reference" type="application/json" data-configuration="{{.DataConfiguration}}">{{.SpecContent}}</script>
        <script src="{{.CDN}}"></script>
      </body>
    </html>`))

type apiReferencePage struct {
	PageTitle         string
	ThemeCSS          template.CSS
	DataConfiguration string
	SpecContent       template.JS
	CDN               string
}

func specContentHandler(specContent interface{}) string {
	switch spec := specContent.(type) {
	case func() map[string]interface{}:
		result := spec()
		jsonData, _ := json.Marshal(result)
		return string(jsonData)
	case map[string]interface{}:
		jsonData, _ := json.Marshal(spec)
		return string(jsonData)
	case string:
		return spec
	default:
		return ""
	}
}

func ApiReferenceHTML(optionsInput *Options) (string, error) {
	options := DefaultOptions(*optionsInput)

	if options.SpecURL == "" && options.SpecContent == nil {
		return "", fmt.Errorf("specURL or specContent must be provided")
	}

	if options.SpecContent == nil && options.SpecURL != "" {

		if strings.HasPrefix(options.SpecURL, "http") {
			content, err := fetchContentFromURL(options.SpecURL)
			if err != nil {
				return "", err
			}
			options.SpecContent = content
		} else {
			urlPath, err := ensureFileURL(options.SpecURL)
			if err != nil {
				return "", err
			}

			content, err := readFileFromURL(urlPath)
			if err != nil {
				return "", err
			}

			options.SpecContent = string(content)
		}
	}

	dataConfig := safeJSONConfiguration(options)
	specContentHTML := strings.ReplaceAll(specContentHandler(options.SpecContent), "<", `\u003c`)

	var pageTitle string

	if options.CustomOptions.PageTitle != "" {
		pageTitle = options.CustomOptions.PageTitle
	} else {
		pageTitle = "Scalar API Reference"
	}

	customThemeCss := CustomThemeCSS

	if options.Theme != "" {
		customThemeCss = ""
	}

	page := apiReferencePage{
		PageTitle:         pageTitle,
		ThemeCSS:          template.CSS(customThemeCss),
		DataConfiguration: dataConfig,
		SpecContent:       template.JS(specContentHTML),
		CDN:               options.CDN,
	}

	var buf bytes.Buffer
	if err := apiReferenceTemplate.Execute(&buf, page); err != nil {
		return "", err
	}
	return buf.String(), nil
}
