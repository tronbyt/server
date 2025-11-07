function enableLocationSearch(inputElement, resultsElement, hiddenInputElement, onChangeCallback) {
    let timeout = null;
    const apiKey = '49863bd631b84a169b347fafbf128ce6';

    function performSearch(query, isInitialSearch = false) {
        if (query.length === 0) {
            resultsElement.innerHTML = '';
            return;
        }

        const encodedQuery = encodeURIComponent(query);
        const url = `https://api.geoapify.com/v1/geocode/search?text=${encodedQuery}&apiKey=${apiKey}`;

        fetch(url)
            .then(response => response.json())
            .then(data => {
                resultsElement.innerHTML = '';

                if (data.features && data.features.length > 0) {
                    const listItems = data.features.map(feature => {
                        const li = document.createElement('li');
                        const icon = document.createElement('i');
                        icon.className = 'fa-solid fa-location-dot';
                        icon.setAttribute('aria-hidden', 'true');
                        li.appendChild(icon);
                        li.appendChild(document.createTextNode(` ${feature.properties.formatted}`));
                        li.dataset.lat = feature.properties.lat;
                        li.dataset.lon = feature.properties.lon;
                        li.dataset.timezone = feature.properties.timezone?.name;
                        li.dataset.formatted = feature.properties.formatted;
                        // Use city first, fallback to locality, then county, then state, then country
                        li.dataset.locality = feature.properties.city || feature.properties.locality || feature.properties.county || feature.properties.state || feature.properties.country || feature.properties.formatted;
                        li.dataset.placeId = feature.properties.place_id;

                        li.addEventListener('click', function () {
                            inputElement.value = this.dataset.formatted;
                            resultsElement.innerHTML = ''; // Clear results after click
                            const locationData = {
                                locality: this.dataset.locality,
                                description: this.dataset.formatted,
                                timezone: this.dataset.timezone,
                                lat: this.dataset.lat,
                                lng: this.dataset.lon
                            };
                            if (this.dataset.placeId) {
                                locationData.place_id = this.dataset.placeId;
                            }
                            const hiddenValue = JSON.stringify(locationData);
                            hiddenInputElement.value = hiddenValue;

                            if (onChangeCallback) {
                                onChangeCallback(hiddenValue);
                            }
                        });

                        resultsElement.appendChild(li);
                        return li;
                    });

                    if (isInitialSearch) {
                        const exactMatchLi = listItems.find(li => li.dataset.formatted === query);
                        if (exactMatchLi) {
                            exactMatchLi.click();
                        } else if (listItems.length > 0) {
                            listItems[0].click();
                        }
                    }
                } else {
                    resultsElement.innerHTML = `<li>{{ _('No results found') }}</li>`;
                }
            })
            .catch(error => console.error('Error fetching location data:', error));
    }

    inputElement.addEventListener('input', function () {
        clearTimeout(timeout);
        const query = this.value.trim();
        timeout = setTimeout(() => {
            performSearch(query, false);
        }, 300);
    });

    // Perform initial search if there's a value in the input field on load
    // BUT only if there's no existing location data in the hidden field
    const initialQuery = inputElement.value.trim();
    const existingLocationData = hiddenInputElement.value.trim();
    if (initialQuery.length > 0 && (!existingLocationData || existingLocationData === '{}')) {
        performSearch(initialQuery, true);
    }
}
