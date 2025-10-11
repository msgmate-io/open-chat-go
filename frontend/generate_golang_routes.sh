#!/bin/sh

# Initialize JSON array
echo '[' > routes.json

# First entry flag to handle commas
first=true

find dist/client -name "*.html" | while read -r file; do
    # Strip dist/client prefix and index.html suffix
    route=$(echo "$file" | sed 's|dist/client||' | sed 's|/index\.html$||' | sed 's|\.html$||')
    
    # Skip some routes
    case "${route}" in
        "/404"|"" ) continue ;;
        *         ) echo "  \"$route\"," >> routes.json
    esac
done

# Remove trailing comma from the last entry and close JSON array
sed -i '$ s/,$//' routes.json
echo ']' >> routes.json
