/* --- App Card Interaction Logic --- */
/* Handles expand/collapse functionality */

document.addEventListener('DOMContentLoaded', () => {
    // Initialize Lucide icons on page load
    if (window.lucide && typeof lucide.createIcons === 'function') {
        lucide.createIcons();
        console.log('Lucide icons initialized');
    }

    // Initialize click handlers for full view headers (collapse on click)
    document.querySelectorAll('.full-view .card-header').forEach(header => {
        header.style.cursor = 'pointer';
    });
});

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
    if (!menu) return;

    const isCurrentlyHidden = menu.classList.contains('hidden');

    // Close all other open menus first
    document.querySelectorAll('.dropdown-menu').forEach(m => {
        if (m.id !== `device-menu-${iname}`) {
            m.classList.add('hidden');
        }
    });

    // Toggle current menu
    menu.classList.toggle('hidden');

    // If we're opening the menu, add click-outside listener
    if (isCurrentlyHidden) {
        const closeHandler = function(e) {
            // Don't close if clicking inside the menu
            if (menu.contains(e.target)) return;
            // Don't close if clicking the toggle button
            const toggleBtn = menu.previousElementSibling;
            if (toggleBtn && toggleBtn.contains(e.target)) return;

            menu.classList.add('hidden');
            document.removeEventListener('click', closeHandler);
        };

        // Delay adding the listener to avoid immediate triggering
        setTimeout(() => {
            document.addEventListener('click', closeHandler);
        }, 10);
    }
}
