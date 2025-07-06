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
                    let exactMatchFound = false;
                    data.features.forEach((feature, index) => {
                        const li = document.createElement('li');
                        li.textContent = `📍 ${feature.properties.formatted}`;
                        li.dataset.lat = feature.properties.lat;
                        li.dataset.lon = feature.properties.lon;
                        li.dataset.timezone = feature.properties.timezone?.name;
                        li.dataset.name = feature.properties.formatted;

                        li.addEventListener('click', function () {
                            inputElement.value = this.dataset.name;
                            resultsElement.innerHTML = '';
                            const hiddenValue = JSON.stringify({
                                name: this.dataset.name,
                                timezone: this.dataset.timezone,
                                lat: this.dataset.lat,
                                lng: this.dataset.lon
                            });
                            hiddenInputElement.value = hiddenValue;

                            if (onChangeCallback) {
                                onChangeCallback(hiddenValue); // Simplified callback
                            }
                        });

                        resultsElement.appendChild(li);

                        if (isInitialSearch && feature.properties.formatted === query) {
                            exactMatchFound = true;
                            // Simulate click on the exact match
                            li.click(); // This will trigger the above event listener
                        }
                    });

                    if (isInitialSearch && !exactMatchFound && resultsElement.firstChild) {
                        // If no exact match was found during initial search, and there are results, click the first one.
                        resultsElement.firstChild.click(); // This will trigger the event listener on the first child
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
    const initialQuery = inputElement.value.trim();
    if (initialQuery.length > 0) {
        performSearch(initialQuery, true);
    }
}
