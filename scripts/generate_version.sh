#!/bin/bash

# Script to generate version.json file with git information
# This script is designed to be run in GitHub Actions or locally

set -e

VERSION_FILE="tronbyt_server/version.json"

# Function to get git tag or commit hash
get_version_info() {
    local version="dev"
    local commit_hash=""
    local tag=""
    local branch=""
    
    # Check if we're in a git repository
    if git rev-parse --git-dir > /dev/null 2>&1; then
        # Get current commit hash
        commit_hash=$(git rev-parse HEAD 2>/dev/null || echo "")
        
        # Get current branch
        branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")
        
        # Check if we're on a tag
        tag=$(git describe --exact-match --tags HEAD 2>/dev/null || echo "")
        
        if [ -n "$tag" ]; then
            # We're on a tag, use the tag as version
            version="$tag"
        elif [ -n "$commit_hash" ]; then
            # Use short commit hash with branch
            short_hash=$(echo "$commit_hash" | cut -c1-7)
            if [ -n "$branch" ] && [ "$branch" != "HEAD" ]; then
                version="${branch}-${short_hash}"
            else
                version="$short_hash"
            fi
        fi
    fi
    
    # If we're in GitHub Actions, use environment variables if available
    if [ -n "$GITHUB_REF" ]; then
        if [[ "$GITHUB_REF" == refs/tags/* ]]; then
            # This is a tag build
            version="${GITHUB_REF#refs/tags/}"
            tag="$version"
        elif [[ "$GITHUB_REF" == refs/heads/* ]]; then
            # This is a branch build
            branch="${GITHUB_REF#refs/heads/}"
            if [ -n "$GITHUB_SHA" ]; then
                short_hash=$(echo "$GITHUB_SHA" | cut -c1-7)
                version="${branch}-${short_hash}"
                commit_hash="$GITHUB_SHA"
            fi
        fi
    fi
    
    echo "$version" "$commit_hash" "$tag" "$branch"
}

# Get version information
read -r version commit_hash tag branch <<< "$(get_version_info)"

# Create the version.json file
cat > "$VERSION_FILE" << EOF
{
    "version": "$version",
    "commit_hash": "$commit_hash",
    "tag": "$tag",
    "branch": "$branch",
    "build_date": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
}
EOF

echo "Generated version file: $VERSION_FILE"
echo "Version: $version"
echo "Commit: $commit_hash"
echo "Tag: $tag"
echo "Branch: $branch"
