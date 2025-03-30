function enableLocationSearch(inputElement, resultsElement, hiddenInputElement, onChangeCallback) {
    let timeout = null;

    inputElement.addEventListener('input', function () {
        clearTimeout(timeout);
        const query = this.value.trim();
        if (query.length === 0) {
            resultsElement.innerHTML = '';
            return;
        }

        timeout = setTimeout(() => {
            const apiKey = '49863bd631b84a169b347fafbf128ce6';
            const encodedQuery = encodeURIComponent(query);
            const url = `https://api.geoapify.com/v1/geocode/search?text=${encodedQuery}&apiKey=${apiKey}`;

            fetch(url)
                .then(response => response.json())
                .then(data => {
                    resultsElement.innerHTML = '';

                    if (data.features && data.features.length > 0) {
                        data.features.forEach(feature => {
                            const li = document.createElement('li');
                            li.textContent = feature.properties.formatted;
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
                                    onChangeCallback(hiddenValue);
                                }
                            });

                            resultsElement.appendChild(li);
                        });
                    } else {
                        resultsElement.innerHTML = `<li>{{ _('No results found') }}</li>`;
                    }
                })
                .catch(error => console.error('Error fetching location data:', error));
        }, 300);
    });
}
