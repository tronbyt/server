#!/bin/bash
# Extracts translation strings from source code.
# Requires goi18n: go install github.com/nicksnyder/go-i18n/v2/goi18n@latest

# Note: goi18n extract primarily works on Go source files.
# For templates, you might need to manually ensure keys match or use a custom extractor.

goi18n extract -sourceLanguage en -outdir web/i18n/ .
