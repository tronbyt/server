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

// Add autoUpdate state tracking
const autoUpdateStates = {};
const autoUpdateIntervals = {};

// Initialize auto-update for all devices when page loads
document.addEventListener('DOMContentLoaded', function() {
  const autoUpdateCheckboxes = document.querySelectorAll('[id^="autoUpdate-"]');
  autoUpdateCheckboxes.forEach(checkbox => {
    const deviceId = checkbox.id.replace('autoUpdate-', '');
    toggleAutoUpdate(deviceId);
  });
});

function toggleAutoUpdate(deviceId) {
  const checkbox = document.getElementById(`autoUpdate-${deviceId}`);

  if (checkbox.checked) {
    // Start auto-updating
    autoUpdateStates[deviceId] = true;
    autoUpdateIntervals[deviceId] = setInterval(() => {
      reloadImage(deviceId);
    }, 5000); // Update every 5 seconds
  } else {
    // Stop auto-updating
    autoUpdateStates[deviceId] = false;
    if (autoUpdateIntervals[deviceId]) {
      clearInterval(autoUpdateIntervals[deviceId]);
    }
  }
}

function reloadImage(deviceId) {
  const currentWebpImg = document.getElementById(`currentWebp-${deviceId}`);
  const timestamp = new Date().getTime(); // Prevent caching
  currentWebpImg.src = `${currentWebpImg.dataset.src}?t=${timestamp}`;
}


function toggleAppsCollapse(deviceId) {
  const appsList = document.getElementById(`appsList-${deviceId}`);
  const collapseBtn = document.getElementById(`collapseBtn-${deviceId}`);
  
  if (appsList.classList.contains("collapsed")) {
    // Expand the apps list
    appsList.classList.remove("collapsed");
    appsList.style.maxHeight = "none";
    appsList.style.overflow = "visible";
    appsList.style.padding = ""; // Reset padding to default
    collapseBtn.innerHTML = '<i class="fas fa-chevron-up"></i> Collapse Apps';
    collapseBtn.title = 'Collapse Apps';
    
    // Save preferences
    const prefs = loadDevicePreferences(deviceId);
    prefs.collapsed = false;
    saveDevicePreferences(deviceId, prefs);
  } else {
    // Collapse the apps list completely
    appsList.classList.add("collapsed");
    appsList.style.maxHeight = "0px";
    appsList.style.overflow = "hidden";
    appsList.style.padding = "0";
    collapseBtn.innerHTML = '<i class="fas fa-chevron-down"></i> Expand Apps';
    collapseBtn.title = 'Expand Apps';
    
    // Save preferences
    const prefs = loadDevicePreferences(deviceId);
    prefs.collapsed = true;
    saveDevicePreferences(deviceId, prefs);
  }
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
    const viewMode = isGridView ? 'grid' : 'list';
    
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
document.addEventListener('DOMContentLoaded', function() {
  initializeDragAndDrop();
  initializeViewToggles();
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

function restoreDevicePreferences(deviceId) {
  const prefs = loadDevicePreferences(deviceId);
  const appsList = document.getElementById(`appsList-${deviceId}`);
  const listBtn = document.getElementById(`listViewBtn-${deviceId}`);
  const gridBtn = document.getElementById(`gridViewBtn-${deviceId}`);
  const collapseBtn = document.getElementById(`collapseBtn-${deviceId}`);
  
  // Restore view mode
  if (prefs.viewMode === 'grid') {
    // Update button states
    gridBtn.classList.add('active');
    listBtn.classList.remove('active');
    
    // Update container classes
    appsList.classList.remove('apps-list-view');
    appsList.classList.add('apps-grid-view');
  } else {
    // Default to list view
    listBtn.classList.add('active');
    gridBtn.classList.remove('active');
    
    appsList.classList.remove('apps-grid-view');
    appsList.classList.add('apps-list-view');
  }
  
  // Restore collapse state
  if (prefs.collapsed) {
    appsList.classList.add('collapsed');
    appsList.style.maxHeight = '0px';
    appsList.style.overflow = 'hidden';
    appsList.style.padding = '0';
    collapseBtn.innerHTML = '<i class="fas fa-chevron-down"></i> Expand Apps';
    collapseBtn.title = 'Expand Apps';
  } else {
    appsList.classList.remove('collapsed');
    appsList.style.maxHeight = 'none';
    appsList.style.overflow = 'visible';
    appsList.style.padding = '';
    collapseBtn.innerHTML = '<i class="fas fa-chevron-up"></i> Collapse Apps';
    collapseBtn.title = 'Collapse Apps';
  }
}

function switchToListView(deviceId) {
  const appsList = document.getElementById(`appsList-${deviceId}`);
  const listBtn = document.getElementById(`listViewBtn-${deviceId}`);
  const gridBtn = document.getElementById(`gridViewBtn-${deviceId}`);
  
  // Update button states
  listBtn.classList.add('active');
  gridBtn.classList.remove('active');
  
  // Update container classes
  appsList.classList.remove('apps-grid-view');
  appsList.classList.add('apps-list-view');
  
  // Reinitialize drag and drop for the new layout
  initializeDragAndDrop();
  
  // Save preferences
  const prefs = loadDevicePreferences(deviceId);
  prefs.viewMode = 'list';
  saveDevicePreferences(deviceId, prefs);
}

function switchToGridView(deviceId) {
  const appsList = document.getElementById(`appsList-${deviceId}`);
  const listBtn = document.getElementById(`listViewBtn-${deviceId}`);
  const gridBtn = document.getElementById(`gridViewBtn-${deviceId}`);
  
  // Update button states
  gridBtn.classList.add('active');
  listBtn.classList.remove('active');
  
  // Update container classes
  appsList.classList.remove('apps-list-view');
  appsList.classList.add('apps-grid-view');
  
  // Reinitialize drag and drop for the new layout
  initializeDragAndDrop();
  
  // Save preferences
  const prefs = loadDevicePreferences(deviceId);
  prefs.viewMode = 'grid';
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
  });
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
  draggedElement = e.target;
  draggedElement.classList.add('dragging');
  
  // Extract device ID and app iname from the card
  const deviceId = extractDeviceIdFromCard(draggedElement);
  const iname = extractInameFromCard(draggedElement);
  
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
  if (!draggedDeviceId || targetDeviceId !== draggedDeviceId) {
    e.dataTransfer.dropEffect = 'none';
    return;
  }
  
  e.dataTransfer.dropEffect = 'move';
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
  
  // Only allow dropping on cards from the same device
  if (draggedDeviceId && targetDeviceId === draggedDeviceId) {
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
  
  const targetCard = e.currentTarget;
  const targetDeviceId = extractDeviceIdFromCard(targetCard);
  const targetIname = extractInameFromCard(targetCard);
  const container = targetCard.closest('[id^="appsList-"]');
  const isGridView = container && container.classList.contains('apps-grid-view');
  
  // Only allow dropping on cards from the same device
  if (!draggedDeviceId || targetDeviceId !== draggedDeviceId) {
    return;
  }
  
  // Don't allow dropping on the same card
  if (targetIname === draggedIname) {
    return;
  }
  
  if (isGridView) {
    // In grid view, determine insert position based on mouse position
    const rect = targetCard.getBoundingClientRect();
    const cardCenterX = rect.left + (rect.width / 2);
    const cardCenterY = rect.top + (rect.height / 2);
    
    // Determine if we should insert before or after the target
    // For grid, we'll use a simple approach: if mouse is in the right half, insert after
    const insertAfter = e.clientX > cardCenterX;
    
    // Reorder the apps (same as list view)
    reorderApps(draggedDeviceId, draggedIname, targetIname, insertAfter);
  } else {
    // In list view, use the original insert logic
    const rect = targetCard.getBoundingClientRect();
    const midpoint = rect.top + (rect.height / 2);
    const insertAfter = e.clientY > midpoint;
    
    // Reorder the apps
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
  
  // Only allow visual feedback for zones from the same device
  if (!draggedDeviceId || deviceId !== draggedDeviceId) {
    e.dataTransfer.dropEffect = 'none';
    return;
  }
  
  e.dataTransfer.dropEffect = 'move';
}

function handleDropZoneDragEnter(e) {
  e.preventDefault();
  const zone = e.currentTarget;
  const deviceId = zone.getAttribute('data-device-id');
  
  // Only allow dropping on zones from the same device
  if (draggedDeviceId && deviceId === draggedDeviceId) {
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
  
  // Only allow dropping on zones from the same device
  if (!draggedDeviceId || deviceId !== draggedDeviceId) {
    return;
  }
  
  // Get the first or last app in the list to determine target
  const container = zone.parentElement;
  const appCards = container.querySelectorAll('.app-card');
  
  if (appCards.length === 0) {
    return;
  }
  
  let targetIname;
  let insertAfter;
  
  if (position === 'top') {
    targetIname = appCards[0].getAttribute('data-iname');
    insertAfter = false;
  } else { // position === 'bottom'
    targetIname = appCards[appCards.length - 1].getAttribute('data-iname');
    insertAfter = true;
  }
  
  // Reorder the apps
  reorderApps(deviceId, draggedIname, targetIname, insertAfter);
  
  // Clean up
  zone.classList.remove('active');
}
