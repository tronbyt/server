// tronbyt_server/static/js/theme.js
(function() {
    const THEME_STORAGE_KEY = 'theme_preference';
    const FONT_STORAGE_KEY = 'font_preference';
    const themeSelect = document.getElementById('theme-select');
    const mobileThemeSelect = document.getElementById('mobile-theme-select');
    const fontCheckbox = document.getElementById('use_system_font');
    const docElement = document.documentElement; // Usually <html>

    let mediaQueryListener = null;

    function applyTheme(theme) {
        if (theme === 'system') {
            const systemPrefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
            docElement.setAttribute('data-theme', systemPrefersDark ? 'dark' : 'light');
        } else {
            docElement.setAttribute('data-theme', theme);
        }
    }

    function applyFont(useSystem) {
        if (useSystem) {
            docElement.setAttribute('data-font', 'system');
        } else {
            docElement.removeAttribute('data-font');
        }
    }

    function storePreference(key, value) {
        localStorage.setItem(key, value);
    }

    function savePreferenceToServer(theme) {
        // Ensure this endpoint exists and is protected
        fetch('/set_theme_preference', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                // CSRF protection for this POST request relies on the SameSite=Lax cookie policy.
            },
            body: JSON.stringify({ theme: theme })
        })
        .then(response => {
            if (!response.ok) {
                console.error('Server responded with an error:', response.status, response.statusText);
                return response.text().then(text => {
                    try {
                        const errJson = JSON.parse(text);
                        if (errJson && (errJson.message || errJson.error)) {
                           return Promise.reject(errJson);
                        }
                        throw new Error(`Server error: ${response.status} ${response.statusText}. Response: ${text.substring(0, 100)}`);
                    } catch (e) {
                        throw new Error(`Server error: ${response.status} ${response.statusText}. Response: ${text.substring(0, 100)}`);
                    }
                });
            }
            return response.json();
        })
        .then(data => {
            if (data.status !== 'success') {
                console.error('Error saving theme preference:', data.message);
            } else {
                console.log('Theme preference saved to server:', theme);
            }
        })
        .catch(error => console.error('Error saving theme preference:', error));
    }

    function handle2xAppImages () {
        const APP_IMG_2X_WIDTH = 128;

        const processImage = (img) => {
            const applyClass = () => {
                const is2x = img.naturalWidth === APP_IMG_2X_WIDTH;
                const container = img.closest('.app-img');
                if (container) {
                    container.classList.toggle('is-2x', is2x);
                }
            };
            if (img.complete && img.naturalWidth > 0) {
                applyClass();
            } else {
                img.addEventListener('load', applyClass, { once: true });
            }
        };

        document.querySelectorAll('.app-img img').forEach(processImage);

        const observer = new MutationObserver((mutations) => {
            mutations.forEach((mutation) => {
                mutation.addedNodes.forEach((node) => {
                    if (node.nodeType !== 1) return;
                    if (node.matches('.app-img img')) {
                        processImage(node);
                    } else {
                        node.querySelectorAll('.app-img img').forEach(processImage);
                    }
                });
            });
        });

        observer.observe(document.body, { childList: true, subtree: true });
    }

    function handleSystemThemeChange(e) {
        // This function is called when system theme changes.
        // It should only re-apply the theme if 'system' is currently selected.
        const currentTheme = (themeSelect && themeSelect.value) || (mobileThemeSelect && mobileThemeSelect.value);
        if (currentTheme === 'system') {
            applyTheme('system');
        } else if (!themeSelect && !mobileThemeSelect) {
            // For pages without the selector (e.g. login page for anonymous users)
            // If a theme is in local storage, respect it. Otherwise, follow system.
            const localTheme = localStorage.getItem(THEME_STORAGE_KEY);
            if (!localTheme || localTheme === 'system') {
                applyTheme('system');
            }
        }
    }

    function setupSystemThemeListener() {
        if (mediaQueryListener) { // Remove existing listener if any
            mediaQueryListener.removeEventListener('change', handleSystemThemeChange);
        }
        mediaQueryListener = window.matchMedia('(prefers-color-scheme: dark)');
        mediaQueryListener.addEventListener('change', handleSystemThemeChange);
    }

    function removeSystemThemeListener() {
        if (mediaQueryListener) {
            mediaQueryListener.removeEventListener('change', handleSystemThemeChange);
            mediaQueryListener = null;
        }
    }

    function initTheme() {
        const localThemePreference = localStorage.getItem(THEME_STORAGE_KEY);
        const localFontPreference = localStorage.getItem(FONT_STORAGE_KEY);
        // window.currentUserThemePreference and window.currentUserFontPreference should be set in base.html if user is logged in
        const serverUserThemePreference = window.currentUserThemePreference;
        const serverUserFontPreference = window.currentUserFontPreference;

        let effectiveTheme = 'system'; // Default

        if (themeSelect || mobileThemeSelect) { // User is logged in and on a page with the theme selector
            if (localThemePreference) {
                effectiveTheme = localThemePreference;
            } else if (serverUserThemePreference) {
                effectiveTheme = serverUserThemePreference;
            }
            if (themeSelect) themeSelect.value = effectiveTheme;
            if (mobileThemeSelect) mobileThemeSelect.value = effectiveTheme;
        } else { // User is not logged in or on a page without selector (e.g. login page)
            if (localThemePreference) {
                effectiveTheme = localThemePreference;
            }
            // No serverUserThemePreference to check here
        }

        applyTheme(effectiveTheme);
        if (effectiveTheme === 'system') {
            setupSystemThemeListener();
        } else {
            removeSystemThemeListener(); // Ensure no listener if not 'system'
        }
        // For logged-in users, ensure localStorage is updated if server preference was used
        if ((themeSelect || mobileThemeSelect) && serverUserThemePreference && !localThemePreference) {
            storePreference(THEME_STORAGE_KEY, serverUserThemePreference);
        }

        // Font preference initialization
        const effectiveFont = localFontPreference || serverUserFontPreference || 'default';
        if (fontCheckbox) {
            fontCheckbox.checked = (effectiveFont === 'system');
        }
        applyFont(effectiveFont === 'system');
        if (serverUserFontPreference && !localFontPreference) {
            storePreference(FONT_STORAGE_KEY, serverUserFontPreference);
        }

        function setupThemeChangeHandler(selector) {
            if (selector) {
                selector.addEventListener('change', function() {
                    const selectedTheme = this.value;
                    applyTheme(selectedTheme);
                    storePreference(THEME_STORAGE_KEY, selectedTheme);
                    savePreferenceToServer(selectedTheme); // Save to backend for logged-in user

                    // Sync both selectors
                    if (themeSelect && mobileThemeSelect) {
                        if (this === themeSelect) {
                            mobileThemeSelect.value = selectedTheme;
                        } else {
                            themeSelect.value = selectedTheme;
                        }
                    }

                    if (selectedTheme === 'system') {
                        setupSystemThemeListener();
                    } else {
                        removeSystemThemeListener();
                    }
                });
            }
        }

        if (fontCheckbox) {
            fontCheckbox.addEventListener('change', function() {
                const useSystem = this.checked;
                const fontVal = useSystem ? 'system' : 'default';
                applyFont(useSystem);
                storePreference(FONT_STORAGE_KEY, fontVal);
                // Note: The form submission in account.html will save to server.
            });
        }

        setupThemeChangeHandler(themeSelect);
        setupThemeChangeHandler(mobileThemeSelect);
        handle2xAppImages();
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', initTheme);
    } else {
        // DOMContentLoaded has already fired
        initTheme();
    }

})();
