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

  fetch(`/${deviceId}/update_brightness`, {
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

  fetch(`/${deviceId}/update_interval`, {
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
        console.error('Error fetching image for device ' + deviceId, response.status);
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
      console.error('Fetch error for device ' + deviceId, error);
    });
}

// AJAX function to move apps without page reload
function moveApp(deviceId, iname, direction) {
  const formData = new URLSearchParams();
  formData.append('direction', direction);

  fetch(`/${deviceId}/${iname}/moveapp?direction=${direction}`, {
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
        refreshAppsList(deviceId, iname);
      }
    })
    .catch((error) => {
      console.error('Unexpected error:', error);
      alert('An error occurred while moving the app. Please try again.');
    });
}

// Function to refresh only the apps list for a specific device
function refreshAppsList(deviceId, movedAppIname = null) {
  // Get the current apps list container
  const appsListContainer = document.getElementById(`appsList-${deviceId}`);

  // Store current view state
  const isGridView = appsListContainer.classList.contains('apps-grid-view');

  // Fetch the updated page content
  fetch(window.location.href)
    .then(response => response.text())
    .then(html => {
      // Create a temporary DOM element to parse the response
      const parser = new DOMParser();
      const doc = parser.parseFromString(html, 'text/html');

      // Find the updated apps list for this device
      const updatedAppsList = doc.getElementById(`appsList-${deviceId}`);

      if (updatedAppsList) {
        // Replace the current apps list with the updated one
        appsListContainer.innerHTML = updatedAppsList.innerHTML;

        // Restore the view state
        if (isGridView) {
          switchToGridView(deviceId);
        } else {
          switchToListView(deviceId);
        }

        // Reinitialize drag and drop for the new cards
        initializeDragAndDrop();

        // If an app was moved, highlight it with visual feedback
        if (movedAppIname) {
          // Find the moved app card in the updated content
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
    })
    .catch(error => {
      console.error('Error refreshing apps list:', error);
      // Fallback: reload the entire page
      window.location.reload();
    });
}

// AJAX function to toggle pin status
function togglePin(deviceId, iname) {
  fetch(`/${deviceId}/${iname}/toggle_pin`, {
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
        refreshAppsList(deviceId);
      }
    })
    .catch((error) => {
      console.error('Unexpected error:', error);
      alert('An error occurred while toggling pin. Please try again.');
    });
}

// AJAX function to toggle enabled status
function toggleEnabled(deviceId, iname) {
  fetch(`/${deviceId}/${iname}/toggle_enabled`, {
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
        refreshAppsList(deviceId);
      }
    })
    .catch((error) => {
      console.error('Unexpected error:', error);
      alert('An error occurred while toggling enabled status. Please try again.');
    });
}

// AJAX function to duplicate an app
function duplicateApp(deviceId, iname) {
  fetch(`/${deviceId}/${iname}/duplicate`, {
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
        refreshAppsList(deviceId);
      }
    })
    .catch((error) => {
      console.error('Unexpected error:', error);
      alert('An error occurred while duplicating the app. Please try again.');
    });
}

// AJAX function to delete an app
function deleteApp(deviceId, iname) {
  if (!confirm('Delete App?')) {
    return;
  }

  fetch(`/${deviceId}/${iname}/delete`, {
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
        // Refresh the apps list for this device
        refreshAppsList(deviceId);
      }
    })
    .catch((error) => {
      console.error('Unexpected error:', error);
      alert('An error occurred while deleting the app. Please try again.');
    });
}

// AJAX function to preview an app
function previewApp(deviceId, iname, config = null, button = null, translations = null) {
  const url = `/${deviceId}/${iname}/preview`;
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

  fetch(url, options)
    .then(response => {
      if (button) {
        if (response.ok) {
          button.innerHTML = `<i class="fa-solid fa-check" aria-hidden="true"></i> ${translations?.sent || 'Sent'}`;
        } else {
          button.innerHTML = `<i class="fa-solid fa-xmark" aria-hidden="true"></i> ${translations?.failed || 'Failed'}`;
          console.error('Preview request failed with status: ' + response.status);
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

// Drag and Drop functionality
let draggedElement = null;
let draggedDeviceId = null;
let draggedIname = null;

// Initialize drag and drop for all app cards when page loads
document.addEventListener('DOMContentLoaded', function () {
  initializeDragAndDrop();
  initializeViewToggles();
  initializeDeviceInfoToggles();

  const webpImages = document.querySelectorAll('[id^="currentWebp-"]');
  webpImages.forEach(image => {
    const deviceId = image.id.replace('currentWebp-', '');
    // Initial fetch
    pollImageWithEtag(deviceId);
    // Set interval for subsequent polls
    setInterval(() => {
      pollImageWithEtag(deviceId);
    }, 5000);
  });
});

// View Toggle Functions
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
    container.insertBefore(topDropZone, container.firstChild);

    // Add bottom drop zone for list view
    const bottomDropZone = document.createElement('div');
    bottomDropZone.className = 'drop-zone bottom';
    bottomDropZone.setAttribute('data-device-id', deviceId);
    bottomDropZone.setAttribute('data-position', 'bottom');
    container.appendChild(bottomDropZone);

    // Add event listeners to drop zones
    [topDropZone, bottomDropZone].forEach(zone => {
      zone.addEventListener('dragover', handleDropZoneDragOver);
      zone.addEventListener('drop', handleDropZoneDrop);
      zone.addEventListener('dragenter', handleDropZoneDragEnter);
      zone.addEventListener('dragleave', handleDropZoneDragLeave);
    });

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

  e.dataTransfer.effectAllowed = 'move';
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

  // Only allow visual feedback for cards from the same device
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

  fetch(`/${targetDeviceId}/duplicate_from/${sourceDeviceId}/${iname}`, {
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
        refreshAppsList(targetDeviceId);
      }
    })
    .catch((error) => {
      console.error('Unexpected error:', error);
      alert('An error occurred while duplicating the app. Please try again.');
    });
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

  fetch(`/${deviceId}/reorder_apps`, {
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
        refreshAppsList(deviceId, draggedIname);
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

function toggleDropdown(id) {
  var x = document.getElementById(id);
  if (x.className.indexOf("w3-show") == -1) {
    x.className += " w3-show";
  } else {
    x.className = x.className.replace(" w3-show", "");
  }
}

// Close dropdowns when clicking outside
window.onclick = function (event) {
  if (!event.target.closest('.w3-dropdown-click')) {
    var dropdowns = document.getElementsByClassName("w3-dropdown-content");
    for (var i = 0; i < dropdowns.length; i++) {
      var openDropdown = dropdowns[i];
      if (openDropdown.classList.contains('w3-show')) {
        openDropdown.classList.remove('w3-show');
      }
    }
  }
}
