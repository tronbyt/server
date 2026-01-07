// tronbyt_server/static/js/theme.js
(function() {
    const THEME_STORAGE_KEY = 'theme_preference';
    const themeGroup = document.getElementById('theme-group');
    const mobileThemeGroup = document.getElementById('mobile-theme-group');
    const docElement = document.documentElement;

    let mediaQueryListener = null;
    let currentThemeValue = 'system';

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
        fetch('/set_theme_preference', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ theme: theme })
        })
        .catch(error => console.error('Error saving theme preference:', error));
    }

    function updateButtonGroupUI(theme) {
        [themeGroup, mobileThemeGroup].forEach(group => {
            if (!group) return;
            group.querySelectorAll('.theme-btn').forEach(btn => {
                btn.classList.toggle('active', btn.getAttribute('data-theme-value') === theme);
            });
        });
    }

    function handleSystemThemeChange(e) {
        if (currentThemeValue === 'system') {
            applyTheme('system');
        }
    }

    function setupSystemThemeListener() {
        if (mediaQueryListener) {
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

    function handle2xAppImages () {
        const APP_IMG_2X_WIDTH = 128;
        const processImage = (img) => {
            const applyClass = () => {
                const is2x = img.naturalWidth === APP_IMG_2X_WIDTH;
                const container = img.closest('.app-img');
                if (container) container.classList.toggle('is-2x', is2x);
            };
            if (img.complete && img.naturalWidth > 0) applyClass();
            else img.addEventListener('load', applyClass, { once: true });
        };
        document.querySelectorAll('.app-img img').forEach(processImage);
        const observer = new MutationObserver((mutations) => {
            mutations.forEach((mutation) => {
                mutation.addedNodes.forEach((node) => {
                    if (node.nodeType !== 1) return;
                    if (node.matches('.app-img img')) processImage(node);
                    else node.querySelectorAll('.app-img img').forEach(processImage);
                });
            });
        });
        observer.observe(document.body, { childList: true, subtree: true });
    }

    function initTheme() {
        const localPreference = localStorage.getItem(THEME_STORAGE_KEY);
        const serverUserPreference = window.currentUserThemePreference;

        let effectiveTheme = 'system';

        if (themeGroup || mobileThemeGroup) {
            if (localPreference) effectiveTheme = localPreference;
            else if (serverUserPreference) effectiveTheme = serverUserPreference;
        } else if (localPreference) {
            effectiveTheme = localPreference;
        }

        currentThemeValue = effectiveTheme;
        applyTheme(effectiveTheme);
        updateButtonGroupUI(effectiveTheme);

        if (effectiveTheme === 'system') setupSystemThemeListener();
        else removeSystemThemeListener();

        if ((themeGroup || mobileThemeGroup) && serverUserPreference && !localPreference) {
            storePreference(serverUserPreference);
        }

        function setupThemeGroupHandler(group) {
            if (!group) return;
            group.addEventListener('click', function(e) {
                const btn = e.target.closest('.theme-btn');
                if (!btn) return;

                const selectedTheme = btn.getAttribute('data-theme-value');
                currentThemeValue = selectedTheme;
                
                applyTheme(selectedTheme);
                updateButtonGroupUI(selectedTheme);
                storePreference(selectedTheme);
                savePreferenceToServer(selectedTheme);

                if (selectedTheme === 'system') setupSystemThemeListener();
                else removeSystemThemeListener();
            });
        }

        setupThemeGroupHandler(themeGroup);
        setupThemeGroupHandler(mobileThemeGroup);
        handle2xAppImages();
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', initTheme);
    } else {
        initTheme();
    }
})();
