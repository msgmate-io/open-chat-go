#!/bin/bash

echo "// Generated mux routes"
echo 'import "net/http"'
echo ""

find dist/client -name "*.html" | while read -r file; do
    # Strip dist/client prefix and index.html suffix
    route=$(echo "$file" | sed 's|dist/client||' | sed 's|/index\.html$||' | sed 's|\.html$||')
    
    # Ensure routes end with trailing slash if they're not root
    if [ "$route" != "/" ]; then
        route="${route%/}/"
    fi
    
    # Skip 404 route as it's typically handled differently
    if [[ "$route" == "/404" ]]; then
        continue
    fi

    # Generate the mux.Handle line
    echo "mux.Handle(\"$route\", providerMiddlewares(FrontendAuthMiddleware(http.HandlerFunc(FrontendHandler))))"
done
