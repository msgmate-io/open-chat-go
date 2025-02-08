package reference

import (
	"log"
	"net/http"
)

func ScalarReference(w http.ResponseWriter, r *http.Request) {
	htmlContent, err := ApiReferenceHTML(&Options{
		SpecURL: "./docs/swagger.json",
		CustomOptions: CustomOptions{
			PageTitle: "Simple API",
		},
		DarkMode: true,
	})

	if err != nil {
		log.Fatal(err)
	}

	w.Write([]byte(htmlContent))
}

// ScalarReferenceWithContent creates a ScalarReference handler that uses embedded swagger content
func ScalarReferenceWithContent(swaggerContent string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		htmlContent, err := ApiReferenceHTML(&Options{
			SpecContent: swaggerContent,
			CustomOptions: CustomOptions{
				PageTitle: "Simple API",
			},
			DarkMode: true,
		})

		if err != nil {
			log.Fatal(err)
		}

		w.Write([]byte(htmlContent))
	}
}
