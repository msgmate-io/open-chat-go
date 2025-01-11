#!/bin/bash

# Routes to exclude
exclude_routes=("/404/" "/")

# Initialize JSON array
echo '[' > routes.json

# First entry flag to handle commas
first=true

find dist/client -name "*.html" | while read -r file; do
    # Strip dist/client prefix and index.html suffix
    route=$(echo "$file" | sed 's|dist/client||' | sed 's|/index\.html$||' | sed 's|\.html$||')
    
    # Ensure routes end with trailing slash if they're not root
    if [ "$route" != "/" ]; then
        route="${route%/}/"
    fi
    
    # Skip excluded routes
    skip=false
    for excluded in "${exclude_routes[@]}"; do
        if [[ "$route" == "$excluded" ]]; then
            skip=true
            break
        fi
    done
    
    if [ "$skip" = true ]; then
        continue
    fi

    # Write route to JSON file with comma for all entries
    echo "  \"$route\"," >> routes.json
done

# Remove trailing comma from the last entry and close JSON array
sed -i '$ s/,$//' routes.json
echo ']' >> routes.json
