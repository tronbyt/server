// tronbyt_server/static/js/theme.js
(function() {
    const THEME_STORAGE_KEY = 'theme_preference';
    const themeSelect = document.getElementById('theme-select');
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

    function storePreference(theme) {
        localStorage.setItem(THEME_STORAGE_KEY, theme);
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
                console.error('Server responded with an error:', response.status);
                return response.json().then(err => Promise.reject(err));
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

    function handleSystemThemeChange(e) {
        // This function is called when system theme changes.
        // It should only re-apply the theme if 'system' is currently selected.
        if (themeSelect && themeSelect.value === 'system') {
            applyTheme('system');
        } else if (!themeSelect) {
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
        const localPreference = localStorage.getItem(THEME_STORAGE_KEY);
        // window.currentUserThemePreference should be set in base.html if user is logged in
        const serverUserPreference = window.currentUserThemePreference;

        let effectiveTheme = 'system'; // Default

        if (themeSelect) { // User is logged in and on a page with the theme selector
            if (localPreference) {
                effectiveTheme = localPreference;
            } else if (serverUserPreference) {
                effectiveTheme = serverUserPreference;
            }
            themeSelect.value = effectiveTheme;
        } else { // User is not logged in or on a page without selector (e.g. login page)
            if (localPreference) {
                effectiveTheme = localPreference;
            }
            // No serverUserPreference to check here
        }

        applyTheme(effectiveTheme);
        if (effectiveTheme === 'system') {
            setupSystemThemeListener();
        } else {
            removeSystemThemeListener(); // Ensure no listener if not 'system'
        }
        // For logged-in users, ensure localStorage is updated if server preference was used
        if (themeSelect && serverUserPreference && !localPreference) {
            storePreference(serverUserPreference);
        }


        if (themeSelect) {
            themeSelect.addEventListener('change', function() {
                const selectedTheme = this.value;
                applyTheme(selectedTheme);
                storePreference(selectedTheme);
                savePreferenceToServer(selectedTheme); // Save to backend for logged-in user

                if (selectedTheme === 'system') {
                    setupSystemThemeListener();
                } else {
                    removeSystemThemeListener();
                }
            });
        }
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', initTheme);
    } else {
        // DOMContentLoaded has already fired
        initTheme();
    }

})();
