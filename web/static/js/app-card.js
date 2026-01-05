/* --- App Card Interaction Logic --- */
/* Handles expand/collapse and AJAX updates for enable/pin without full reload */

document.addEventListener('DOMContentLoaded', () => {
    // Initialize Lucide icons with retry for slow CDN
    initLucideIcons();
    
    // Initialize click handlers for full view headers (collapse on click)
    document.querySelectorAll('.full-view .card-header').forEach(header => {
        header.style.cursor = 'pointer';
    });
});

// Initialize Lucide icons with retry mechanism
function initLucideIcons(retries = 5) {
    if (window.lucide && typeof lucide.createIcons === 'function') {
        lucide.createIcons();
        console.log('Lucide icons initialized');
    } else if (retries > 0) {
        // CDN might be slow, retry after 500ms
        setTimeout(() => initLucideIcons(retries - 1), 500);
        console.log('Waiting for Lucide to load... retries left:', retries);
    } else {
        console.error('Lucide icons failed to load from CDN');
    }
}

// Helper to refresh Lucide icons after DOM updates
function refreshIcons() {
    if (window.lucide) {
        lucide.createIcons();
    }
}

/**
 * Toggle between compact and full view
 * Called from onclick on compact-view cards
 */
function toggleAppCardView(iname, event) {
    // Don't toggle if clicking on action buttons
    if (event && event.target.closest('.btn-tool, .btn-action-lg, .btn-action-sm, button, a, select')) {
        return;
    }
    
    const compactCard = document.getElementById(`app-card-${iname}`);
    const fullCard = document.getElementById(`app-card-full-${iname}`);
    
    if (!compactCard || !fullCard) return;
    
    const isExpanded = compactCard.dataset.expanded === 'true';
    
    if (isExpanded) {
        // Collapse
        collapseAppCard(iname);
    } else {
        // Expand
        expandAppCard(iname);
    }
}

/**
 * Expand card to full view
 */
function expandAppCard(iname) {
    const compactCard = document.getElementById(`app-card-${iname}`);
    const fullCard = document.getElementById(`app-card-full-${iname}`);
    
    if (!compactCard || !fullCard) return;
    
    compactCard.classList.add('hidden');
    compactCard.dataset.expanded = 'true';
    fullCard.classList.remove('hidden');
}

/**
 * Collapse card to compact view
 */
function collapseAppCard(iname) {
    const compactCard = document.getElementById(`app-card-${iname}`);
    const fullCard = document.getElementById(`app-card-full-${iname}`);
    
    if (!compactCard || !fullCard) return;
    
    fullCard.classList.add('hidden');
    compactCard.classList.remove('hidden');
    compactCard.dataset.expanded = 'false';
}

/**
 * Toggle the "Copy to" device dropdown menu
 */
function toggleDeviceMenu(iname) {
    const menu = document.getElementById(`device-menu-${iname}`);
    if (menu) {
        menu.classList.toggle('hidden');
    }
}

/**
 * AJAX-based toggle for enabled state (no full page reload)
 * This overrides/wraps the existing toggleEnabled function
 */
const originalToggleEnabled = typeof toggleEnabled === 'function' ? toggleEnabled : null;

function toggleEnabledAjax(deviceId, iname) {
    // Make AJAX call
    fetch(`/devices/${deviceId}/${iname}/toggle`, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
            'X-Requested-With': 'XMLHttpRequest'
        }
    })
    .then(response => response.json())
    .then(data => {
        if (data.success || data.enabled !== undefined) {
            const isEnabled = data.enabled;
            updateCardEnabledState(iname, isEnabled);
        } else {
            // Fallback to page reload if response is unexpected
            location.reload();
        }
    })
    .catch(err => {
        console.error('Toggle failed:', err);
        // Fallback to original function or reload
        if (originalToggleEnabled) {
            originalToggleEnabled(deviceId, iname);
        } else {
            location.reload();
        }
    });
}

/**
 * Update card UI after enable/disable toggle
 */
function updateCardEnabledState(iname, isEnabled) {
    const compactCard = document.getElementById(`app-card-${iname}`);
    const fullCard = document.getElementById(`app-card-full-${iname}`);
    
    // Compact view updates
    if (compactCard) {
        // Status badge
        const statusBadge = compactCard.querySelector('.status-badge');
        if (statusBadge) {
            statusBadge.classList.toggle('is-enabled', isEnabled);
            statusBadge.classList.toggle('gray', !isEnabled);
            const label = statusBadge.querySelector('.text-enable-label');
            if (label) {
                label.textContent = isEnabled ? 'ENABLED' : 'DISABLED';
            }
        }
        
        // Play/Pause button
        const playBtn = compactCard.querySelector('.btn-play');
        if (playBtn) {
            playBtn.classList.toggle('is-enabled', isEnabled);
            const playIcon = playBtn.querySelector('.play-icon');
            const pauseIcon = playBtn.querySelector('.pause-icon');
            if (playIcon) playIcon.classList.toggle('hidden', isEnabled);
            if (pauseIcon) pauseIcon.classList.toggle('hidden', !isEnabled);
        }
    }
    
    // Full view updates
    if (fullCard) {
        // Toggle button in header
        const toggleBtn = fullCard.querySelector('.btn-toggle-main');
        if (toggleBtn) {
            toggleBtn.classList.toggle('is-enabled', isEnabled);
            const label = toggleBtn.querySelector('.text-enable-label');
            if (label) {
                label.textContent = isEnabled ? 'ENABLED' : 'DISABLED';
            }
        }
        
        // Large play button
        const playBtnLg = fullCard.querySelector('.btn-action-lg.btn-play');
        if (playBtnLg) {
            playBtnLg.classList.toggle('is-enabled', isEnabled);
            const playIcon = playBtnLg.querySelector('.play-icon');
            const pauseIcon = playBtnLg.querySelector('.pause-icon');
            const label = playBtnLg.querySelector('span');
            if (playIcon) playIcon.classList.toggle('hidden', isEnabled);
            if (pauseIcon) pauseIcon.classList.toggle('hidden', !isEnabled);
            if (label) label.textContent = isEnabled ? 'Disable' : 'Enable';
        }
    }
}

/**
 * AJAX-based toggle for pinned state (no full page reload)
 */
const originalTogglePin = typeof togglePin === 'function' ? togglePin : null;

function togglePinAjax(deviceId, iname) {
    fetch(`/devices/${deviceId}/${iname}/pin`, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
            'X-Requested-With': 'XMLHttpRequest'
        }
    })
    .then(response => response.json())
    .then(data => {
        if (data.success || data.pinned !== undefined) {
            const isPinned = data.pinned;
            updateCardPinnedState(iname, isPinned);
        } else {
            location.reload();
        }
    })
    .catch(err => {
        console.error('Pin toggle failed:', err);
        if (originalTogglePin) {
            originalTogglePin(deviceId, iname);
        } else {
            location.reload();
        }
    });
}

/**
 * Update card UI after pin/unpin toggle
 */
function updateCardPinnedState(iname, isPinned) {
    const compactCard = document.getElementById(`app-card-${iname}`);
    const fullCard = document.getElementById(`app-card-full-${iname}`);
    
    // Compact view updates
    if (compactCard) {
        const pinBtn = compactCard.querySelector('.btn-pin');
        if (pinBtn) {
            pinBtn.classList.toggle('is-pinned', isPinned);
            const icon = pinBtn.querySelector('i');
            if (icon) {
                icon.classList.toggle('fa-thumbtack-slash', isPinned);
                icon.classList.toggle('fa-thumbtack', !isPinned);
            }
        }
        
        // Update status badge to show PINNED
        const statusArea = compactCard.querySelector('.compact-status');
        if (statusArea) {
            if (isPinned) {
                statusArea.innerHTML = '<div class="badge black">PINNED</div>';
            } else {
                // Need to restore enabled/disabled badge - for now just reload
                // This is a simplification; full implementation would track state
            }
        }
    }
    
    // Full view updates
    if (fullCard) {
        const pinBtn = fullCard.querySelector('.btn-action-sm.btn-pin');
        if (pinBtn) {
            pinBtn.classList.toggle('is-pinned', isPinned);
            const icon = pinBtn.querySelector('i');
            const label = pinBtn.querySelector('span');
            if (icon) {
                icon.classList.toggle('fa-thumbtack-slash', isPinned);
                icon.classList.toggle('fa-thumbtack', !isPinned);
            }
            if (label) {
                label.textContent = isPinned ? 'Unpin' : 'Pin';
            }
        }
        
        // Update header badge
        const headerBadges = fullCard.querySelector('.card-header .flex.items-center.gap-3');
        if (headerBadges && isPinned) {
            // Add PINNED badge if not exists
            if (!headerBadges.querySelector('.badge.black')) {
                const badge = document.createElement('div');
                badge.className = 'badge black';
                badge.textContent = 'PINNED';
                headerBadges.appendChild(badge);
            }
        }
    }
}
