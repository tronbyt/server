// Global variables for addapp filtering/sorting
let isInitialLoad = true;
let sortType = 'system';
let hideInstalled = false;
let showBroken = false;
let isProcessing = false;

// Debounce function
function debounce(func, wait) {
  let timeout;
  return function executedFunction(...args) {
    const later = () => {
      clearTimeout(timeout);
      func(...args);
    };
    clearTimeout(timeout);
    timeout = setTimeout(later, wait);
  };
}

const debouncedSearch = debounce(() => {
  applyFilters();
}, 300);



// Function to update the numeric display in real-time (on slider move)
function updateBrightnessValue(deviceId, brightness) {
  document.getElementById(`brightnessValue-${deviceId}`).innerText = brightness;
  // Update active button state
  const buttons = document.querySelectorAll(`#brightness-panel-${deviceId} .brightness-btn`);
  buttons.forEach(btn => {
    if (parseInt(btn.dataset.brightness) === parseInt(brightness)) {
      btn.classList.add('active');
    } else {
      btn.classList.remove('active');
    }
  });
}

function updateIntervalValue(deviceId, interval) {
  document.getElementById(`intervalValue-${deviceId}`).innerText = interval;
}

// Function to send the value to the server only when the slider is released
function updateBrightness(deviceId, brightness) {
  const formData = new URLSearchParams();
  formData.append('brightness', brightness);

  fetch(`/devices/${deviceId}/update_brightness`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
    },
    body: formData.toString()
  })
    .then(response => {
      if (!response.ok) {
        console.error('Failed to update brightness');
      } else {
        console.log('Brightness updated successfully to', brightness);
      }
    })
    .catch((error) => {
      console.error('Unexpected error:', error);
    });
}

// Function to handle brightness button clicks
function setBrightness(deviceId, brightness) {
  updateBrightnessValue(deviceId, brightness);
  updateBrightness(deviceId, brightness);
}

// Function to send the value to the server only when the slider is released
function updateInterval(deviceId, interval) {
  const formData = new URLSearchParams();
  formData.append('interval', interval);

  fetch(`/devices/${deviceId}/update_interval`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
    },
    body: formData.toString()
  })
    .then(response => {
      if (!response.ok) {
        console.error('Failed to update interval');
      } else {
        console.log('interval updated successfully to', interval);
      }
    })
    .catch((error) => {
      console.error('Unexpected error:', error);
    });
}

// Function to toggle the visibility of the device details
function toggleDetails(deviceId) {
  const details = document.getElementById(`details-${deviceId}`);
  const toggleBtn = document.getElementById(`toggleBtn-${deviceId}`);
  if (details.classList.contains("hidden")) {
    details.classList.remove("hidden");
    details.classList.add("visible");
    toggleBtn.textContent = "Hide Details";
  } else {
    details.classList.remove("visible");
    details.classList.add("hidden");
    toggleBtn.textContent = "Show Details";
  }
}

const etags = {};

function pollImageWithEtag(deviceId) {
  const img = document.getElementById('currentWebp-' + deviceId);
  if (!img) return;

  const url = img.dataset.src;
  const headers = new Headers();
  if (etags[deviceId]) {
    headers.append('If-None-Match', etags[deviceId]);
  }

  fetch(url, { headers: headers, cache: 'no-cache' })
    .then(response => {
      if (response.status === 200) {
        const newEtag = response.headers.get('ETag');
        if (newEtag) {
          etags[deviceId] = newEtag;
        }
        return response.blob();
      } else if (response.status === 304) {
        // Not modified, do nothing
        return null;
      } else {
        // Handle other errors
        console.error('Error fetching image for device', deviceId, response.status);
        return null;
      }
    })
    .then(blob => {
      if (blob) {
        const oldSrc = img.src;
        // Check if oldSrc is a blob URL and revoke it to prevent memory leaks
        if (oldSrc.startsWith('blob:')) {
          URL.revokeObjectURL(oldSrc);
        }
        img.src = URL.createObjectURL(blob);
      }
    })
    .catch(error => {
      console.error('Fetch error for device', deviceId, error);
    });
}

// AJAX function to move apps without page reload
function moveApp(deviceId, iname, direction) {
  const formData = new URLSearchParams();
  formData.append('direction', direction);

  fetch(`/devices/${deviceId}/${iname}/moveapp?direction=${direction}`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
    },
    body: formData.toString()
  })
    .then(response => {
      if (!response.ok) {
        console.error('Failed to move app');
        alert('Failed to move app. Please try again.');
      } else {
        console.log('App moved successfully');
        // Refresh the apps list for this device and highlight the moved app
        refreshDeviceCard(deviceId, iname);
      }
    })
    .catch((error) => {
      console.error('Unexpected error:', error);
      alert('An error occurred while moving the app. Please try again.');
    });
}

// Function to refresh the entire device card
function refreshDeviceCard(deviceId, movedAppIname = null) {
  // Get the current device card container
  const deviceCard = document.getElementById(`device-card-${deviceId}`);
  if (!deviceCard) {
    console.error(`Device card not found for device ${deviceId}`);
    return;
  }

  // Fetch the updated device card content
  fetch(`/?device_id=${deviceId}&partial=device_card`, { headers: { 'Cache-Control': 'no-cache' } })
    .then(response => response.text())
    .then(html => {
      // Create a temporary DOM element to parse the response
      const parser = new DOMParser();
      const doc = parser.parseFromString(html, 'text/html');

      // Find the updated device card for this device
      const updatedDeviceCard = doc.getElementById(`device-card-${deviceId}`);

      if (updatedDeviceCard) {
        // Replace the current device card with the updated one
        deviceCard.replaceWith(updatedDeviceCard);

        // Restore the view state (list/grid/collapsed)
        restoreDevicePreferences(deviceId);

        // Reinitialize UI components
        initializeDragAndDrop();
        initializeDeviceInfoToggles();
        pollImageWithEtag(deviceId);

        // If an app was moved, highlight it with visual feedback
        if (movedAppIname) {
          // Find the moved app card in the updated content
          const appsListContainer = document.getElementById(`appsList-${deviceId}`);
          if (appsListContainer) {
            const appCards = appsListContainer.querySelectorAll('.app-card');
            appCards.forEach(card => {
              // Check if this card has the moved app's iname
              if (card.getAttribute('data-iname') === movedAppIname) {
                // Add the moved class to trigger the animation
                card.classList.add('app-card-moved');

                // Remove the class after animation completes
                setTimeout(() => {
                  card.classList.remove('app-card-moved');
                }, 1500); // Match the animation duration
              }
            });
          }
        }
      }
    })
    .catch(error => {
      console.error('Error refreshing device card:', error);
      // Fallback: reload the entire page
      window.location.reload();
    });
}

// AJAX function to toggle pin status
function togglePin(deviceId, iname) {
  fetch(`/devices/${deviceId}/${iname}/toggle_pin`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
    }
  })
    .then(response => {
      if (!response.ok) {
        console.error('Failed to toggle pin');
        alert('Failed to toggle pin. Please try again.');
      } else {
        console.log('Pin toggled successfully');
        // Refresh the apps list for this device
        refreshDeviceCard(deviceId);
      }
    })
    .catch((error) => {
      console.error('Unexpected error:', error);
      alert('An error occurred while toggling pin. Please try again.');
    });
}

// AJAX function to toggle enabled status
function toggleEnabled(deviceId, iname) {
  fetch(`/devices/${deviceId}/${iname}/toggle_enabled`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
    }
  })
    .then(response => {
      if (!response.ok) {
        console.error('Failed to toggle enabled status');
        alert('Failed to toggle enabled status. Please try again.');
      } else {
        console.log('Enabled status toggled successfully');
        // Refresh the apps list for this device
        refreshDeviceCard(deviceId);
      }
    })
    .catch((error) => {
      console.error('Unexpected error:', error);
      alert('An error occurred while toggling enabled status. Please try again.');
    });
}

// AJAX function to duplicate an app
function duplicateApp(deviceId, iname) {
  fetch(`/devices/${deviceId}/${iname}/duplicate`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
    }
  })
    .then(response => {
      if (!response.ok) {
        console.error('Failed to duplicate app');
        alert('Failed to duplicate app. Please try again.');
      } else {
        console.log('App duplicated successfully');
        // Refresh the apps list for this device
        refreshDeviceCard(deviceId);
      }
    })
    .catch((error) => {
      console.error('Unexpected error:', error);
      alert('An error occurred while duplicating the app. Please try again.');
    });
}

// AJAX function to delete an app
function deleteApp(deviceId, iname, redirectAfterDelete = false, confirmMessage = null) {
  if (!confirm(confirmMessage || 'Delete App?')) {
    return;
  }

  fetch(`/devices/${deviceId}/${iname}/delete`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
    }
  })
    .then(response => {
      if (!response.ok) {
        console.error('Failed to delete app');
        alert('Failed to delete app. Please try again.');
      } else {
        console.log('App deleted successfully');
        if (redirectAfterDelete) {
          window.location.href = "/";
        } else {
          refreshDeviceCard(deviceId);
        }
      }
    })
    .catch((error) => {
      console.error('Unexpected error:', error);
      alert('An error occurred while deleting the app. Please try again.');
    });
}

function duplicateAppToDevice(sourceDeviceId, iname, targetDeviceId, targetIname, insertAfter) {
  console.log('duplicateAppToDevice called:', { sourceDeviceId, iname, targetDeviceId, targetIname, insertAfter });

  if (!sourceDeviceId || !iname || !targetDeviceId) {
    console.error('Missing required parameters for duplicateAppToDevice');
    return;
  }

  const formData = new URLSearchParams();
  if (targetIname) {
    formData.append('target_iname', targetIname);
  }
  formData.append('insert_after', insertAfter ? 'true' : 'false');

  fetch(`/devices/${targetDeviceId}/apps/duplicate_from/${sourceDeviceId}/${iname}`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
    },
    body: formData.toString()
  })
    .then(response => {
      if (!response.ok) {
        console.error('Failed to duplicate app to device');
        alert('Failed to duplicate app to device. Please try again.');
      } else {
        console.log('App duplicated successfully to new device');
        // Refresh the apps list for the TARGET device
        refreshDeviceCard(targetDeviceId);
      }
    })
    .catch((error) => {
      console.error('Unexpected error:', error);
      alert('An error occurred while duplicating the app. Please try again.');
    });
}

// AJAX function to preview an app
function previewApp(deviceId, iname, config = null, button = null, translations = null) {
  const url = `/devices/${deviceId}/${iname}/preview`;
  let options = {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    }
  };

  if (config) {
    options.body = JSON.stringify(config);
  }

  let originalButtonContent = null;
  if (button) {
    originalButtonContent = button.innerHTML;
    button.innerHTML = `<i class="fa-solid fa-spinner fa-spin" aria-hidden="true"></i> ${translations?.previewing || 'Previewing...'}`;
    button.disabled = true;
  }

  return fetch(url, options)
    .then(response => {
      if (button) {
        if (response.ok) {
          button.innerHTML = `<i class="fa-solid fa-check" aria-hidden="true"></i> ${translations?.sent || 'Sent'}`;
        } else {
          button.innerHTML = `<i class="fa-solid fa-xmark" aria-hidden="true"></i> ${translations?.failed || 'Failed'}`;
          console.error('Preview request failed with status:', response.status);
        }
      }
    })
    .catch(error => {
      console.error('Error sending preview request:', error);
      if (button) {
        button.innerHTML = `<i class="fa-solid fa-xmark" aria-hidden="true"></i> ${translations?.failed || 'Failed'}`;
      }
    })
    .finally(() => {
      if (button) {
        setTimeout(() => {
          button.innerHTML = originalButtonContent;
          button.disabled = false;
        }, 2000);
      }
    });
}

// Cookie utility functions
function setCookie(name, value, days = 365) {
  const expires = new Date();
  expires.setTime(expires.getTime() + (days * 24 * 60 * 60 * 1000));
  document.cookie = `${name}=${encodeURIComponent(value)};expires=${expires.toUTCString()};path=/`;
}

function getCookie(name) {
  const nameEQ = name + "=";
  const ca = document.cookie.split(';');
  for (let i = 0; i < ca.length; i++) {
    let c = ca[i];
    while (c.charAt(0) === ' ') c = c.substring(1, c.length);
    if (c.indexOf(nameEQ) === 0) return decodeURIComponent(c.substring(nameEQ.length, c.length));
  }
  return null;
}

function deleteCookie(name) {
  document.cookie = `${name}=;expires=Thu, 01 Jan 1970 00:00:00 UTC;path=/;`;
}

// User preferences functions
function saveDevicePreferences(deviceId, preferences) {
  const key = `device_prefs_${deviceId}`;
  setCookie(key, JSON.stringify(preferences));
}

function loadDevicePreferences(deviceId) {
  const key = `device_prefs_${deviceId}`;
  const prefs = getCookie(key);
  return prefs ? JSON.parse(prefs) : { collapsed: false, viewMode: 'list' };
}

function saveAllDevicePreferences() {
  const appListContainers = document.querySelectorAll('[id^="appsList-"]');
  appListContainers.forEach(container => {
    const deviceId = container.id.replace('appsList-', '');
    const isCollapsed = container.classList.contains('collapsed');
    const isGridView = container.classList.contains('apps-grid-view');

    let viewMode = 'list';
    if (isGridView) {
      viewMode = 'grid';
    } else if (isCollapsed) {
      viewMode = 'collapsed';
    }

    saveDevicePreferences(deviceId, {
      collapsed: isCollapsed,
      viewMode: viewMode
    });
  });
}



// ADDAPP FUNCTIONS START

function preventSubmitOnEnter(event) {
  if (event.key === "Enter" && !event.isComposing) {
    event.preventDefault();
  }
}

function searchApps(searchId, gridId) {
  debouncedSearch(searchId);
}

function toggleInstalledApps(searchId) {
  const checkbox = document.getElementById('hide_installed_' + searchId);
  hideInstalled = checkbox ? checkbox.checked : false;
  applyFilters();
}

function toggleBrokenApps(searchId) {
  const checkbox = document.getElementById('show_broken_' + searchId);
  showBroken = checkbox ? checkbox.checked : false;
  applyFilters();
}

function sortApps(searchId) {
  sortType = document.getElementById('sort_' + searchId).value;
  applyFilters();
}

// Optimized sorting function
function sortItems(items, sortTypeParam = null) {
  const currentSortType = sortTypeParam || sortType;

  // Parse date function for newest sort
  const parseDate = (dateStr) => {
    if (!dateStr) return new Date(0);
    // Handle format "YYYY-MM-DD HH:MM"
    const parts = dateStr.split(' ');
    if (parts.length === 2) {
      const [datePart, timePart] = parts;
      const [year, month, day] = datePart.split('-').map(Number);
      const [hour, minute] = timePart.split(':').map(Number);
      return new Date(year, month - 1, day, hour, minute);
    }
    return new Date(dateStr);
  };

  // Create a copy to avoid mutating the original array
  const itemsCopy = [...items];
  const sortedItems = itemsCopy.sort((a, b) => {
    const nameA = (a.getAttribute('data-name') || '').toLowerCase();
    const nameB = (b.getAttribute('data-name') || '').toLowerCase();
    const installedA = a.getAttribute('data-installed') === 'true';
    const installedB = b.getAttribute('data-installed') === 'true';
    const dateA = a.getAttribute('data-date') || '';
    const dateB = b.getAttribute('data-date') || '';

    switch (currentSortType) {
      case 'alphabetical':
        return nameA.localeCompare(nameB);
      case 'rev-alphabetical':
        return nameB.localeCompare(nameA);
      case 'newest':
        // Convert date strings to Date objects for proper chronological sorting
        const dateA_obj = parseDate(dateA);
        const dateB_obj = parseDate(dateB);
        const dateComparison = dateB_obj.getTime() - dateA_obj.getTime();
        return dateComparison === 0 ? nameA.localeCompare(nameB) : dateComparison;
      case 'system':
      default:
        if (installedA && !installedB) return -1;
        if (!installedA && installedB) return 1;
        return nameA.localeCompare(nameB);
    }
  });

  return sortedItems;
}

// Function to show all items in a grid
function showAllItems(grid) {
  const allItems = Array.from(grid.getElementsByClassName('app-item'));
  allItems.forEach(item => {
    item.style.display = 'block';
  });
}

// Robust filtering system that handles all combinations
function applyFilters() {
  if (isInitialLoad || isProcessing) return;

  isProcessing = true;

  // Use requestAnimationFrame to batch DOM updates and prevent UI blocking
  requestAnimationFrame(() => {
    try {
      const grids = document.querySelectorAll('.app-grid');

      grids.forEach(grid => {
        if (!grid) return;
        const searchId = grid.id.replace('_app_grid', '_search');

        // Get current filter values
        const searchInput = document.getElementById(searchId);
        const currentSearchFilter = searchInput ? searchInput.value.toLowerCase().trim() : '';

        const hideInstalledCheckbox = document.getElementById('hide_installed_' + searchId);
        const currentHideInstalled = hideInstalledCheckbox ? hideInstalledCheckbox.checked : false;

        const showBrokenCheckbox = document.getElementById('show_broken_' + searchId);
        const currentShowBroken = showBrokenCheckbox ? showBrokenCheckbox.checked : false;

        const sortSelect = document.getElementById('sort_' + searchId);
        const currentSortType = sortSelect ? sortSelect.value : 'system';

        // Get all app items from the grid
        const allItems = Array.from(grid.getElementsByClassName('app-item'));

        // Filter items based on all criteria
        const filteredItems = allItems.filter(item => {
          const isInstalled = item.getAttribute('data-installed') === 'true';
          const isBroken = item.getAttribute('data-broken') === 'true';
          const name = (item.getAttribute('data-name') || '').toLowerCase();
          const author = (item.getAttribute('data-author') || '').toLowerCase();
          const summary = (item.querySelector('p')?.textContent || '').toLowerCase();

          // Apply search filter (search name, summary, and author if search begins with @)
          if (currentSearchFilter) {
            if (currentSearchFilter.startsWith("@")) {
              if (!author.includes(currentSearchFilter.substring(1))) {
                return false;
              }
            } else {
              if (!name.includes(currentSearchFilter) && !summary.includes(currentSearchFilter)) {
                return false;
              }
            }
          }

          // Apply hide filters
          if (currentHideInstalled && isInstalled) return false;
          if (!currentShowBroken && isBroken) return false;

          return true;
        });

        // Sort filtered items
        const sortedItems = sortItems(filteredItems, currentSortType);

        // Use requestAnimationFrame to prevent UI blocking during DOM reordering
        requestAnimationFrame(() => {
          // Batch DOM operations for better performance
          const fragment = document.createDocumentFragment();

          // Add sorted items to fragment
          sortedItems.forEach((item, index) => {
            fragment.appendChild(item);
            item.style.display = 'block';
          });

          // Append all at once to reduce reflows
          grid.appendChild(fragment);

          // Hide any remaining items that weren't in the filtered list
          allItems.forEach(item => {
            if (!sortedItems.includes(item)) {
              item.style.display = 'none';
            }
          });
        });

        // Update virtual scrolling state
        updateVirtualScrolling(grid, sortedItems, currentSearchFilter);

        // Update installed class states
        updateItemStates(allItems);
      });
    } finally {
      isProcessing = false;
    }
  });
}

// Update virtual scrolling based on current state
function updateVirtualScrolling(grid, sortedItems, hasSearchFilter) {
  if (!grid._virtualScrolling) return;

  const ITEMS_PER_PAGE = grid._virtualScrolling.ITEMS_PER_PAGE;
  const shouldUseVirtualScrolling = sortedItems.length > ITEMS_PER_PAGE && !hasSearchFilter;

  if (shouldUseVirtualScrolling) {
    // Hide items beyond the first page
    sortedItems.forEach((item, index) => {
      if (index >= ITEMS_PER_PAGE) {
        item.style.display = 'none';
      }
    });

    // Update virtual scrolling state
    grid._virtualScrolling.visibleEnd = Math.min(ITEMS_PER_PAGE, sortedItems.length);
    grid._virtualScrolling.isLoading = false;
    grid._virtualScrolling.items = sortedItems;
  } else {
    // Show all items
    grid._virtualScrolling.visibleEnd = sortedItems.length;
    grid._virtualScrolling.isLoading = false;
    grid._virtualScrolling.items = sortedItems;
  }
}

// Update item states (installed class, etc.)
function updateItemStates(allItems) {
  allItems.forEach(item => {
    if (!item) return;
    const isInstalled = item.getAttribute('data-installed') === 'true';
    if (isInstalled) {
      item.classList.add('installed');
    } else {
      item.classList.remove('installed');
    }
  });
}

// Intersection Observer for lazy loading images
function setupLazyLoading() {
  const imageObserver = new IntersectionObserver((entries, observer) => {
    entries.forEach(entry => {
      if (entry.isIntersecting) {
        const img = entry.target;
        if (img.dataset.src) {
          img.src = img.dataset.src;
          img.removeAttribute('data-src');
          // Add loaded class when image loads
          img.onload = function () {
            img.classList.add('loaded');
          };
          // Also add loaded class immediately for cached images
          if (img.complete) {
            img.classList.add('loaded');
          }
          observer.unobserve(img);
        }
      }
    });
  }, {
    rootMargin: '50px 0px',
    threshold: 0.1
  });

  document.querySelectorAll('img[data-src]').forEach(img => {
    imageObserver.observe(img);
  });
}

// Virtual scrolling for large lists (optional optimization)
function setupVirtualScrolling() {
  const grids = document.querySelectorAll('.app-grid');
  const ITEMS_PER_PAGE = 50; // Load 50 items at a time

  grids.forEach(grid => {
    if (!grid) return;
    const items = Array.from(grid.getElementsByClassName('app-item'));
    if (!items || items.length <= ITEMS_PER_PAGE) return; // Skip if list is small

    let visibleStart = 0;
    let visibleEnd = ITEMS_PER_PAGE;
    let isLoading = false;

    // Store reference to this grid's virtual scrolling state
    grid._virtualScrolling = {
      visibleStart,
      visibleEnd,
      isLoading,
      items,
      ITEMS_PER_PAGE
    };

    // Initially hide items beyond the first page only for system sort and large lists
    if (items && items.length > ITEMS_PER_PAGE) {
      // Check if we should apply virtual scrolling initially
      const shouldApplyVirtualScrolling = sortType === 'system' ||
        (document.getElementById('system_search') && !document.getElementById('system_search').value.trim());

      if (shouldApplyVirtualScrolling) {
        items.forEach((item, index) => {
          if (!item) return;
          if (index >= ITEMS_PER_PAGE) {
            item.style.display = 'none';
          }
        });
      }
    }

    // Function to load more items
    function loadMoreItems() {
      if (isLoading || visibleEnd >= items.length) return;

      isLoading = true;
      const nextStart = visibleEnd;
      const nextEnd = Math.min(visibleEnd + ITEMS_PER_PAGE, items.length);

      // Use requestAnimationFrame to batch DOM updates and prevent UI blocking
      requestAnimationFrame(() => {
        for (let i = nextStart; i < nextEnd; i++) {
          if (items[i]) {
            items[i].style.display = 'block';
          }
        }
        visibleEnd = nextEnd;
        isLoading = false;

        // Update the stored state
        grid._virtualScrolling.visibleEnd = visibleEnd;
        grid._virtualScrolling.isLoading = isLoading;

        // Re-observe the new last item
        updateObserver();
      });
    }

    // Function to load more items from filtered results
    function loadMoreFilteredItems() {
      if (!grid._virtualScrolling || grid._virtualScrolling.isLoading) return;

      const filteredItems = grid._virtualScrolling.items;
      const currentVisibleEnd = grid._virtualScrolling.visibleEnd;
      const ITEMS_PER_PAGE = grid._virtualScrolling.ITEMS_PER_PAGE;

      if (currentVisibleEnd >= filteredItems.length) return;

      grid._virtualScrolling.isLoading = true;
      const nextStart = currentVisibleEnd;
      const nextEnd = Math.min(currentVisibleEnd + ITEMS_PER_PAGE, filteredItems.length);

      // Use requestAnimationFrame to batch DOM updates and prevent UI blocking
      requestAnimationFrame(() => {
        for (let i = nextStart; i < nextEnd; i++) {
          if (filteredItems[i]) {
            filteredItems[i].style.display = 'block';
          }
        }
        grid._virtualScrolling.visibleEnd = nextEnd;
        grid._virtualScrolling.isLoading = false;

        // Re-observe the new last item
        updateObserver();
      });
    }

    // Function to update the observer
    function updateObserver() {
      if (visibleEnd >= items.length) {
        paginationObserver.disconnect();
        return;
      }

      // Observe the last few visible items to ensure we catch scroll events
      const observeStart = Math.max(0, visibleEnd - 3);
      const observeEnd = Math.min(visibleEnd, items.length);

      for (let i = observeStart; i < observeEnd; i++) {
        if (items[i] && items[i].style.display !== 'none') {
          paginationObserver.observe(items[i]);
        }
      }
    }

    // Set up intersection observer for pagination
    const paginationObserver = new IntersectionObserver((entries) => {
      entries.forEach(entry => {
        if (entry.isIntersecting) {
          // Check if we have filtered items (filters are active)
          if (grid._virtualScrolling && grid._virtualScrolling.items && grid._virtualScrolling.items.length > 0) {
            loadMoreFilteredItems();
          } else {
            loadMoreItems();
          }
        }
      });
    }, {
      rootMargin: '200px 0px' // Increased margin to trigger earlier
    });

    // Also observe the grid container for scroll events
    const gridObserver = new IntersectionObserver((entries) => {
      entries.forEach(entry => {
        if (entry.isIntersecting) {
          // Check if we're near the bottom and need to load more
          const rect = entry.boundingClientRect;
          const viewportHeight = window.innerHeight;

          // If the grid is visible and we're near the bottom, load more
          if (rect.bottom < viewportHeight + 300) {
            // Check if we have filtered items (filters are active)
            if (grid._virtualScrolling && grid._virtualScrolling.items && grid._virtualScrolling.items.length > 0) {
              loadMoreFilteredItems();
            } else {
              loadMoreItems();
            }
          }
        }
      });
    }, {
      rootMargin: '300px 0px'
    });

    // Initial setup
    updateObserver();
    gridObserver.observe(grid);

    // Also listen for scroll events as a fallback
    let scrollTimeout;
    window.addEventListener('scroll', () => {
      clearTimeout(scrollTimeout);
      scrollTimeout = setTimeout(() => {
        const rect = grid.getBoundingClientRect();
        const viewportHeight = window.innerHeight;

        // If grid is visible and we're near the bottom, load more
        if (rect.top < viewportHeight && rect.bottom > 0 && rect.bottom < viewportHeight + 500) {
          // Check if we have filtered items (filters are active)
          if (grid._virtualScrolling && grid._virtualScrolling.items && grid._virtualScrolling.items.length > 0) {
            loadMoreFilteredItems();
          } else {
            loadMoreItems();
          }
        }
      }, 100);
    });
  });
}

function initializeDragAndDrop() {
  // Find all app cards and make them draggable
  const appCards = document.querySelectorAll('.app-card');
  appCards.forEach(card => {
    setupDragAndDrop(card);
  });

  // Add drop zones at the top and bottom of each device's app list
  addDropZones();
}

function addDropZones() {
  // Find all app list containers
  const appListContainers = document.querySelectorAll('[id^="appsList-"]');

  appListContainers.forEach(container => {
    const deviceId = container.id.replace('appsList-', '');
    const isGridView = container.classList.contains('apps-grid-view');

    // Clean up existing drop zones first
    const existingDropZones = container.querySelectorAll('.drop-zone');
    existingDropZones.forEach(zone => zone.remove());

    if (isGridView) {
      // For grid view, don't add drop zones - we'll handle it differently
      return;
    }

    // Add top drop zone for list view
    const topDropZone = document.createElement('div');
    topDropZone.className = 'drop-zone top';
    topDropZone.setAttribute('data-device-id', deviceId);
    topDropZone.setAttribute('data-position', 'top');

    topDropZone.addEventListener('dragover', handleDropZoneDragOver);
    topDropZone.addEventListener('dragenter', handleDropZoneDragEnter);
    topDropZone.addEventListener('dragleave', handleDropZoneDragLeave);
    topDropZone.addEventListener('drop', handleDropZoneDrop);

    container.insertBefore(topDropZone, container.firstChild);

    // Add bottom drop zone for list view
    const bottomDropZone = document.createElement('div');
    bottomDropZone.className = 'drop-zone bottom';
    bottomDropZone.setAttribute('data-device-id', deviceId);
    bottomDropZone.setAttribute('data-position', 'bottom');

    bottomDropZone.addEventListener('dragover', handleDropZoneDragOver);
    bottomDropZone.addEventListener('dragenter', handleDropZoneDragEnter);
    bottomDropZone.addEventListener('dragleave', handleDropZoneDragLeave);
    bottomDropZone.addEventListener('drop', handleDropZoneDrop);

    container.appendChild(bottomDropZone);

    // Add event listeners to container for dropping on empty space
    container.addEventListener('dragover', handleContainerDragOver);
    container.addEventListener('drop', handleContainerDrop);
    container.addEventListener('dragenter', handleContainerDragEnter);
    container.addEventListener('dragleave', handleContainerDragLeave);
  });
}

function handleContainerDragOver(e) {
  // If we are over an app card or drop zone, let their handlers handle it
  if (e.target.closest('.app-card') || e.target.closest('.drop-zone')) {
    return;
  }

  e.preventDefault();
  const container = e.currentTarget;
  const deviceId = container.id.replace('appsList-', '');

  if (!draggedDeviceId) {
    e.dataTransfer.dropEffect = 'none';
    return;
  }

  if (deviceId !== draggedDeviceId) {
    e.dataTransfer.dropEffect = 'copy';
  } else {
    // If same device, and we are just hovering over container (not specific card),
    // we technically could "move" to end, but usually reordering is specific.
    // However, for consistency let's allow "move" (which will act as append)
    e.dataTransfer.dropEffect = 'move';
  }
}

function handleContainerDragEnter(e) {
  // If we are over an app card or drop zone, let their handlers handle it
  if (e.target.closest('.app-card') || e.target.closest('.drop-zone')) {
    return;
  }

  e.preventDefault();
  const container = e.currentTarget;
  if (draggedDeviceId) {
    container.classList.add('drag-over-container');
  }
}

function handleContainerDragLeave(e) {
  const container = e.currentTarget;
  // Only remove if we are leaving the container, not entering a child
  if (!container.contains(e.relatedTarget)) {
    container.classList.remove('drag-over-container');
  }
}

function handleContainerDrop(e) {
  // If we are over an app card or drop zone, let their handlers handle it
  if (e.target.closest('.app-card') || e.target.closest('.drop-zone')) {
    return;
  }

  e.preventDefault();
  const container = e.currentTarget;
  const deviceId = container.id.replace('appsList-', '');

  container.classList.remove('drag-over-container');

  if (!draggedDeviceId) {
    return;
  }

  // Dropping on container means append to list
  // Target iname is null, insertAfter doesn't matter much but false implies "append if not found" logic in backend
  // Actually backend logic: if target_iname not found/null -> append.

  const targetIname = null;
  const insertAfter = false;

  if (deviceId !== draggedDeviceId) {
    duplicateAppToDevice(draggedDeviceId, draggedIname, deviceId, targetIname, insertAfter);
  } else {
    // Same device - "move to end" or do nothing?
    // If we drop on empty space of same device, maybe move to end?
    // reorderApps requires target_iname.
    // So we need to find the last app.
    const appCards = container.querySelectorAll('.app-card');
    if (appCards.length > 0) {
      const lastApp = appCards[appCards.length - 1];
      const lastIname = lastApp.getAttribute('data-iname');

      // If we are already the last app, do nothing
      if (lastIname === draggedIname) return;

      reorderApps(deviceId, draggedIname, lastIname, true);
    }
  }
}

function setupDragAndDrop(card) {
  // Make the card draggable
  card.draggable = true;

  // Add drag event listeners
  card.addEventListener('dragstart', handleDragStart);
  card.addEventListener('dragend', handleDragEnd);
  card.addEventListener('dragover', handleDragOver);
  card.addEventListener('drop', handleDrop);
  card.addEventListener('dragenter', handleDragEnter);
  card.addEventListener('dragleave', handleDragLeave);

  // Prevent drag on buttons and links
  const buttons = card.querySelectorAll('button, a, input');
  buttons.forEach(button => {
    button.addEventListener('mousedown', (e) => {
      e.stopPropagation();
    });
    button.addEventListener('dragstart', (e) => {
      e.preventDefault();
    });
  });
}

function handleDragStart(e) {
  console.log('handleDragStart called');
  draggedElement = e.target;
  draggedElement.classList.add('dragging');

  // Extract device ID and app iname from the card
  const deviceId = extractDeviceIdFromCard(draggedElement);
  const iname = extractInameFromCard(draggedElement);

  console.log('Drag started:', { deviceId, iname });

  if (deviceId && iname) {
    draggedDeviceId = deviceId;
    draggedIname = iname;
    e.dataTransfer.setData('text/plain', `${deviceId}:${iname}`);
  }

  e.dataTransfer.effectAllowed = 'copyMove';
}

function handleDragEnd(e) {
  draggedElement.classList.remove('dragging');
  draggedElement = null;
  draggedDeviceId = null;
  draggedIname = null;

  // Clean up all drag-over classes
  document.querySelectorAll('.app-card').forEach(card => {
    card.classList.remove('drag-over', 'drag-over-bottom');
  });

  // Clean up drop zones
  document.querySelectorAll('.drop-zone').forEach(zone => {
    zone.classList.remove('active');
  });
}

function handleDragOver(e) {
  e.preventDefault();

  const card = e.currentTarget;
  const targetDeviceId = extractDeviceIdFromCard(card);

  if (!draggedDeviceId) {
    e.dataTransfer.dropEffect = 'none';
    return;
  }

  // Set drop effect based on whether it's the same device (move) or different (copy)
  if (targetDeviceId !== draggedDeviceId) {
    e.dataTransfer.dropEffect = 'copy';
  } else {
    e.dataTransfer.dropEffect = 'move';
  }
  const container = card.closest('[id^="appsList-"]');
  const isGridView = container && container.classList.contains('apps-grid-view');

  if (isGridView) {
    // In grid view, show left/right insertion feedback
    const rect = card.getBoundingClientRect();
    const cardCenterX = rect.left + (rect.width / 2);

    // Determine if we should insert before or after the target
    if (e.clientX < cardCenterX) {
      card.classList.remove('drag-over-bottom');
      card.classList.add('drag-over');
    } else {
      card.classList.remove('drag-over');
      card.classList.add('drag-over-bottom');
    }
  } else {
    // In list view, use the original top/bottom logic
    const rect = card.getBoundingClientRect();
    const midpoint = rect.top + (rect.height / 2);

    // Determine if we're dragging over the top or bottom half
    if (e.clientY < midpoint) {
      card.classList.remove('drag-over-bottom');
      card.classList.add('drag-over');
    } else {
      card.classList.remove('drag-over');
      card.classList.add('drag-over-bottom');
    }
  }
}

function handleDragEnter(e) {
  e.preventDefault();
  const card = e.currentTarget;
  const targetDeviceId = extractDeviceIdFromCard(card);

  // Only allow dropping on cards from capable devices
  if (draggedDeviceId) {
    card.classList.add('drag-over');
  } else {
    // Remove any existing visual feedback from invalid targets
    card.classList.remove('drag-over', 'drag-over-bottom');
  }
}

function handleDragLeave(e) {
  const card = e.currentTarget;
  card.classList.remove('drag-over', 'drag-over-bottom');
}

function handleDrop(e) {
  e.preventDefault();
  console.log('handleDrop called');

  const targetCard = e.currentTarget;
  const targetDeviceId = extractDeviceIdFromCard(targetCard);
  const targetIname = extractInameFromCard(targetCard);
  const container = targetCard.closest('[id^="appsList-"]');
  const isGridView = container && container.classList.contains('apps-grid-view');

  console.log('Drop target:', { targetDeviceId, targetIname, draggedDeviceId, draggedIname });

  // Only allow dropping if valid drag
  if (!draggedDeviceId) {
    return;
  }

  let insertAfter = false;

  if (isGridView) {
    // In grid view, determine insert position based on mouse position
    const rect = targetCard.getBoundingClientRect();
    const cardCenterX = rect.left + (rect.width / 2);

    // Determine if we should insert before or after the target
    // For grid, we'll use a simple approach: if mouse is in the right half, insert after
    insertAfter = e.clientX > cardCenterX;
  } else {
    // In list view, use the original insert logic
    const rect = targetCard.getBoundingClientRect();
    const midpoint = rect.top + (rect.height / 2);
    insertAfter = e.clientY > midpoint;
  }

  if (targetDeviceId !== draggedDeviceId) {
    // Different device - Duplicate!
    duplicateAppToDevice(draggedDeviceId, draggedIname, targetDeviceId, targetIname, insertAfter);
  } else {
    // Same device - Reorder
    // Don't allow dropping on the same card
    if (targetIname === draggedIname) {
      console.log('Drop rejected: same card');
      return;
    }
    reorderApps(draggedDeviceId, draggedIname, targetIname, insertAfter);
  }

  // Clean up
  targetCard.classList.remove('drag-over', 'drag-over-bottom');
}

function extractDeviceIdFromCard(card) {
  // Look for device ID in the card's data attributes or parent container
  const deviceContainer = card.closest('[id^="appsList-"]');
  if (deviceContainer) {
    return deviceContainer.id.replace('appsList-', '');
  }
  return null;
}

function extractInameFromCard(card) {
  // Get the iname from the data attribute
  return card.getAttribute('data-iname');
}

function reorderApps(deviceId, draggedIname, targetIname, insertAfter) {
  console.log('reorderApps called:', { deviceId, draggedIname, targetIname, insertAfter });

  if (!deviceId || !draggedIname || !targetIname) {
    console.error('Missing required parameters for reorderApps');
    return;
  }

  const formData = new URLSearchParams();
  formData.append('dragged_iname', draggedIname);
  formData.append('target_iname', targetIname);
  formData.append('insert_after', insertAfter ? 'true' : 'false');

  fetch(`/devices/${deviceId}/reorder_apps`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
    },
    body: formData.toString()
  })
    .then(response => {
      if (!response.ok) {
        console.error('Failed to reorder apps');
        alert('Failed to reorder apps. Please try again.');
      } else {
        console.log('Apps reordered successfully');
        // Refresh the apps list for this device and highlight the moved app
        refreshDeviceCard(deviceId, draggedIname);
      }
    })
    .catch((error) => {
      console.error('Unexpected error:', error);
      alert('An error occurred while reordering apps. Please try again.');
    });
}


// Drop zone event handlers
function handleDropZoneDragOver(e) {
  e.preventDefault();

  const zone = e.currentTarget;
  const deviceId = zone.getAttribute('data-device-id');

  // Only allow visual feedback for zones if valid drag
  if (!draggedDeviceId) {
    e.dataTransfer.dropEffect = 'none';
    return;
  }

  if (deviceId !== draggedDeviceId) {
    e.dataTransfer.dropEffect = 'copy';
  } else {
    e.dataTransfer.dropEffect = 'move';
  }
}

function handleDropZoneDragEnter(e) {
  e.preventDefault();
  const zone = e.currentTarget;
  const deviceId = zone.getAttribute('data-device-id');

  // Only allow dropping on zones if valid drag
  if (draggedDeviceId) {
    zone.classList.add('active');
  } else {
    // Remove any existing visual feedback from invalid targets
    zone.classList.remove('active');
  }
}

function handleDropZoneDragLeave(e) {
  const zone = e.currentTarget;
  zone.classList.remove('active');
}

function handleDropZoneDrop(e) {
  e.preventDefault();

  const zone = e.currentTarget;
  const deviceId = zone.getAttribute('data-device-id');
  const position = zone.getAttribute('data-position');

  // Only allow dropping on zones if valid drag
  if (!draggedDeviceId) {
    return;
  }

  // Get the first or last app in the list to determine target
  const container = zone.parentElement;
  const appCards = container.querySelectorAll('.app-card');

  let targetIname = null;
  let insertAfter = false;

  if (appCards.length > 0) {
    if (position === 'top') {
      targetIname = appCards[0].getAttribute('data-iname');
      insertAfter = false;
    } else { // position === 'bottom'
      targetIname = appCards[appCards.length - 1].getAttribute('data-iname');
      insertAfter = true;
    }
  } else {
    // Empty list - we are appending to empty device
    targetIname = null;
    insertAfter = false;
  }

  // Reorder the apps
  // Check if cross-device or same device
  if (deviceId !== draggedDeviceId) {
    duplicateAppToDevice(draggedDeviceId, draggedIname, deviceId, targetIname, insertAfter);
  } else {
    if (appCards.length > 0) {
      reorderApps(deviceId, draggedIname, targetIname, insertAfter);
    } else {
      // Should not happen for same device reorder (cannot be empty if we are dragging an app from it)
      console.warn("Attempted to reorder in empty device list");
    }
  }

  // Clean up
  zone.classList.remove('active');
}

function toggleDeviceInfo(button) {
  const content = document.getElementById(button.getAttribute('aria-controls'));
  if (!content) {
    return;
  }
  const icon = button.querySelector('i');
  const isExpanded = button.getAttribute('aria-expanded') === 'true';

  button.setAttribute('aria-expanded', !isExpanded);
  content.classList.toggle('is-expanded');

  if (!isExpanded) {
    content.style.maxHeight = content.scrollHeight + "px";
    if (icon) {
      icon.classList.replace('fa-chevron-down', 'fa-chevron-up');
    }
  } else {
    content.style.maxHeight = '0px';
    if (icon) {
      icon.classList.replace('fa-chevron-up', 'fa-chevron-down');
    }
  }
}

// Function to mark app as broken (development mode only)
function markAppAsBroken(appName, packageName, event) {
  event.stopPropagation();
  event.preventDefault();

  if (!confirm("Mark '" + appName + "' as broken? This will add 'broken: true' to its manifest.yaml and prevent it from being installed.")) {
    return;
  }

  let url = "/mark_app_broken?app_name=" + encodeURIComponent(appName);
  if (packageName && packageName !== 'None' && packageName !== '') {
    url += "&package_name=" + encodeURIComponent(packageName);
  }

  fetch(url, {
    method: 'POST'
  })
    .then(response => response.json())
    .then(data => {
      if (data.success) {
        alert("App marked as broken successfully!");
        location.reload();
      } else {
        alert("Error: " + data.message);
      }
    })
    .catch(error => {
      console.error('Error:', error);
      alert("Failed to mark app as broken");
    });
}

// Function to unmark app as broken (development mode only)
function unmarkAppAsBroken(appName, packageName, event) {
  event.stopPropagation();
  event.preventDefault();

  if (!confirm("Unmark '" + appName + "' as broken? This will set 'broken: false' in its manifest.yaml and allow it to be installed.")) {
    return;
  }

  let url = "/unmark_app_broken?app_name=" + encodeURIComponent(appName);
  if (packageName && packageName !== 'None' && packageName !== '') {
    url += "&package_name=" + encodeURIComponent(packageName);
  }

  fetch(url, {
    method: 'POST'
  })
    .then(response => response.json())
    .then(data => {
      if (data.success) {
        alert("App unmarked successfully!");
        location.reload();
      } else {
        alert("Error: " + data.message);
      }
    })
    .catch(error => {
      console.error('Error:', error);
      alert("Failed to unmark app");
    });
}

function initializeViewToggles() {
  // Restore saved preferences for all devices
  const appLists = document.querySelectorAll('[id^="appsList-"]');
  appLists.forEach(list => {
    const deviceId = list.id.replace('appsList-', '');
    restoreDevicePreferences(deviceId);
  });
}

function initializeDeviceInfoToggles() {
  document.querySelectorAll('.device-info-toggle').forEach(button => {
    button.addEventListener('click', () => toggleDeviceInfo(button));
  });
}

function restoreDevicePreferences(deviceId) {
  const prefs = loadDevicePreferences(deviceId);
  const appsList = document.getElementById(`appsList-${deviceId}`);
  const listBtn = document.getElementById(`listViewBtn-${deviceId}`);
  const gridBtn = document.getElementById(`gridViewBtn-${deviceId}`);
  const collapsedBtn = document.getElementById(`collapsedViewBtn-${deviceId}`);

  if (!appsList || !listBtn || !gridBtn || !collapsedBtn) return;

  // Restore view mode
  if (prefs.viewMode === 'grid') {
    // Update button states
    gridBtn.classList.add('active');
    listBtn.classList.remove('active');
    collapsedBtn.classList.remove('active');

    // Update container classes
    appsList.classList.remove('apps-list-view', 'collapsed');
    appsList.classList.add('apps-grid-view');
    appsList.style.maxHeight = "none";
    appsList.style.overflow = "visible";
    appsList.style.padding = "";
  } else if (prefs.viewMode === 'collapsed') {
    // Update button states
    collapsedBtn.classList.add('active');
    listBtn.classList.remove('active');
    gridBtn.classList.remove('active');

    // Update container classes - collapse the apps list
    appsList.classList.remove('apps-grid-view');
    appsList.classList.add('apps-list-view', 'collapsed');
    appsList.style.maxHeight = "0";
    appsList.style.overflow = "hidden";
    appsList.style.padding = "0";
  } else {
    // Default to list view
    listBtn.classList.add('active');
    gridBtn.classList.remove('active');
    collapsedBtn.classList.remove('active');

    appsList.classList.remove('apps-grid-view', 'collapsed');
    appsList.classList.add('apps-list-view');
    appsList.style.maxHeight = "none";
    appsList.style.overflow = "visible";
    appsList.style.padding = "";
  }
}

function switchToListView(deviceId) {
  const appsList = document.getElementById(`appsList-${deviceId}`);
  const listBtn = document.getElementById(`listViewBtn-${deviceId}`);
  const gridBtn = document.getElementById(`gridViewBtn-${deviceId}`);
  const collapsedBtn = document.getElementById(`collapsedViewBtn-${deviceId}`);

  // Update button states
  listBtn.classList.add('active');
  gridBtn.classList.remove('active');
  collapsedBtn.classList.remove('active');

  // Update container classes
  appsList.classList.remove('apps-grid-view', 'collapsed');
  appsList.classList.add('apps-list-view');
  appsList.style.maxHeight = "none";
  appsList.style.overflow = "visible";
  appsList.style.padding = "";

  // Reinitialize drag and drop for the new layout
  initializeDragAndDrop();

  // Save preferences
  const prefs = loadDevicePreferences(deviceId);
  prefs.viewMode = 'list';
  prefs.collapsed = false;
  saveDevicePreferences(deviceId, prefs);
}

function switchToGridView(deviceId) {
  const appsList = document.getElementById(`appsList-${deviceId}`);
  const listBtn = document.getElementById(`listViewBtn-${deviceId}`);
  const gridBtn = document.getElementById(`gridViewBtn-${deviceId}`);
  const collapsedBtn = document.getElementById(`collapsedViewBtn-${deviceId}`);

  // Update button states
  gridBtn.classList.add('active');
  listBtn.classList.remove('active');
  collapsedBtn.classList.remove('active');

  // Update container classes
  appsList.classList.remove('apps-list-view', 'collapsed');
  appsList.classList.add('apps-grid-view');
  appsList.style.maxHeight = "none";
  appsList.style.overflow = "visible";
  appsList.style.padding = "";

  // Reinitialize drag and drop for the new layout
  initializeDragAndDrop();

  // Save preferences
  const prefs = loadDevicePreferences(deviceId);
  prefs.viewMode = 'grid';
  prefs.collapsed = false;
  saveDevicePreferences(deviceId, prefs);
}

function switchToCollapsedView(deviceId) {
  const appsList = document.getElementById(`appsList-${deviceId}`);
  const listBtn = document.getElementById(`listViewBtn-${deviceId}`);
  const gridBtn = document.getElementById(`gridViewBtn-${deviceId}`);
  const collapsedBtn = document.getElementById(`collapsedViewBtn-${deviceId}`);

  // Update button states
  collapsedBtn.classList.add('active');
  listBtn.classList.remove('active');
  gridBtn.classList.remove('active');

  // Update container classes - collapse the apps list
  appsList.classList.remove('apps-grid-view');
  appsList.classList.add('apps-list-view', 'collapsed');
  appsList.style.maxHeight = "0";
  appsList.style.overflow = "hidden";
  appsList.style.padding = "0";

  // Save preferences
  const prefs = loadDevicePreferences(deviceId);
  prefs.viewMode = 'collapsed';
  prefs.collapsed = true;
  saveDevicePreferences(deviceId, prefs);
}

// Initialize drag and drop for all app cards when page loads
document.addEventListener('DOMContentLoaded', function () {
  // Common initializations
  initializeDragAndDrop();
  initializeViewToggles();
  initializeDeviceInfoToggles();

  // Polling logic for fallback
  const pollingIntervals = {};

  function startPollingAll() {
    const webpImages = document.querySelectorAll('[id^="currentWebp-"]');
    webpImages.forEach(image => {
      const deviceId = image.id.replace('currentWebp-', '');
      if (!pollingIntervals[deviceId]) {
        pollImageWithEtag(deviceId);
        pollingIntervals[deviceId] = setInterval(() => {
          pollImageWithEtag(deviceId);
        }, 5000);
      }
    });
  }

  function stopPollingAll() {
    Object.keys(pollingIntervals).forEach(deviceId => {
      clearInterval(pollingIntervals[deviceId]);
      delete pollingIntervals[deviceId];
    });
  }

  // Start polling initially (safety net)
  startPollingAll();

  const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const wsUrl = `${wsProtocol}//${window.location.host}/ws`;
  const dashboardWs = new WebSocket(wsUrl);

  dashboardWs.onopen = () => {
    console.log('Dashboard WebSocket connected');
    stopPollingAll(); // Rely on push updates
  };
  function refreshAll() {
    console.log('Dashboard refresh signal received');
    const appListContainers = document.querySelectorAll('[id^="appsList-"]');
    appListContainers.forEach(container => {
      const deviceId = container.id.replace('appsList-', '');
      refreshDeviceCard(deviceId);
      pollImageWithEtag(deviceId);
    });
  }

  dashboardWs.onmessage = (event) => {
    try {
      const msg = JSON.parse(event.data);
      if (msg.type === 'refresh') {
        refreshAll();
      } else if (msg.type === 'apps_changed' && msg.device_id) {
        console.log('Apps changed for device', msg.device_id);
        refreshDeviceCard(msg.device_id);
        pollImageWithEtag(msg.device_id);
      } else if (msg.type === 'device_updated' && msg.device_id) {
        console.log('Device updated', msg.device_id);
        if (msg.payload) {
          if (msg.payload.brightness !== undefined) {
            updateBrightnessValue(msg.device_id, msg.payload.brightness);
          }
          if (msg.payload.interval !== undefined) {
            updateIntervalValue(msg.device_id, msg.payload.interval);
          }
        }
      } else if (msg.type === 'image_updated' && msg.device_id) {
        console.log('Image updated for device', msg.device_id);
        pollImageWithEtag(msg.device_id);
      } else if (msg.type === 'device_deleted' && msg.device_id) {
        console.log('Device deleted', msg.device_id);
        if (window.location.pathname.startsWith('/devices/' + msg.device_id)) {
          window.location.href = "/";
        } else {
          window.location.reload();
        }
      }
    } catch (e) {
      if (event.data === 'refresh') {
        refreshAll();
      }
    }
  };
  dashboardWs.onclose = (event) => {
    console.log('Dashboard WebSocket disconnected', event);
    startPollingAll(); // Fallback to polling
  };
  dashboardWs.onerror = (error) => { console.error('Dashboard WebSocket error:', error); };

  // Addapp specific initializations (if on addapp page)
  if (document.getElementById('addapp_page_identifier')) { // A unique ID for the addapp page content
    setTimeout(() => {
      document.body.classList.add('content-loaded');
      const loadingIndicator = document.getElementById('loading-indicator');
      if (loadingIndicator) loadingIndicator.style.display = 'none';
      isInitialLoad = false;
      setupVirtualScrolling();
      setupLazyLoading();
      applyFilters();
    }, 100);

    // Event delegation for clicks on app items
    document.addEventListener('click', function (e) {
      const appItem = e.target.closest('.app-item');
      if (!appItem) return;

      if (e.target.closest('.delete-upload-btn')) return;

      // Don't allow clicking on broken apps
      if (appItem.classList.contains('broken-app')) {
        return;
      }

      document.querySelectorAll('.app-item').forEach(i => i.classList.remove('selected'));
      appItem.classList.add('selected');

      document.getElementById('selected_app').value = appItem.getAttribute('data-value');
      document.getElementById('selected_app_path').value = appItem.getAttribute('data-path');

      const recInterval = appItem.getAttribute('data-recommended-interval');
      if (recInterval) {
        document.getElementById('uinterval').value = recInterval;
      } else {
        document.getElementById('uinterval').value = 10;
      }

      // Automatically submit the form
      const form = document.getElementById('main_form');
      if (form) {
        form.submit();
      }
    });

    // Enhanced image loading with error handling (for addapp page)
    document.addEventListener('load', function (e) {
      if (e.target.classList.contains('lazy-image')) {
        e.target.classList.add('loaded');
        const skeleton = e.target.parentElement.querySelector('.skeleton-loader');
        if (skeleton) {
          skeleton.style.display = 'none';
        }
      }
    }, true);

    document.addEventListener('error', function (e) {
      if (e.target.classList.contains('lazy-image')) {
        const skeleton = e.target.parentElement.querySelector('.skeleton-loader');
        if (skeleton) {
          skeleton.style.display = 'none';
        }
        e.target.style.display = 'none';
      }
    }, true);
  }
});

function triggerOTA(deviceId, confirmMessage) {
  if (confirm(confirmMessage)) {
    const form = document.createElement('form');
    form.method = 'POST';
    form.action = `/devices/${deviceId}/ota`;
    document.body.appendChild(form);
    form.submit();
  }
}

function triggerOTAWithVersion(deviceId, confirmMessage) {
  if (confirm(confirmMessage)) {
    const form = document.createElement('form');
    form.method = 'POST';
    form.action = `/devices/${deviceId}/ota`;

    const select = document.getElementById('firmware_version_select');
    if (select) {
        const versionInput = document.createElement('input');
        versionInput.type = 'hidden';
        versionInput.name = 'version';
        versionInput.value = select.value;
        form.appendChild(versionInput);
    }

    document.body.appendChild(form);
    form.submit();
  }
}
