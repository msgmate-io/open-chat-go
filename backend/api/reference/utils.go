package reference

// original source: github.com/MarceloPetrucio/go-scalar-api-reference
// forked for customization

import (
	"encoding/json"
	"fmt"
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
	escapedJSON := strings.ReplaceAll(string(jsonData), `"`, `&quot;`)
	return escapedJSON
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
	specContentHTML := specContentHandler(options.SpecContent)

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

	return fmt.Sprintf(`
    <!DOCTYPE html>
    <html>
      <head>
        <title>%s</title>
        <meta charset="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <style>%s</style>
      </head>
      <body>
        <script id="api-reference" type="application/json" data-configuration="%s">%s</script>
        <script src="%s"></script>
      </body>
    </html>
  `, pageTitle, customThemeCss, dataConfig, specContentHTML, options.CDN), nil
}
