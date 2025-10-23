from flask.testing import FlaskClient


def test_dots_svg_defaults(client: FlaskClient) -> None:
    response = client.get("/v0/dots")

    assert response.status_code == 200
    assert response.mimetype == "image/svg+xml"

    body = response.get_data(as_text=True)
    assert 'width="64"' in body
    assert 'height="32"' in body
    assert body.count("<circle") == 64 * 32
    assert '<circle cx="0.5" cy="0.5" r="0.3"/>' in body


def test_dots_svg_custom_parameters(client: FlaskClient) -> None:
    response = client.get("/v0/dots?w=2&h=1&r=0.75")

    assert response.status_code == 200

    body = response.get_data(as_text=True)
    assert 'width="2"' in body
    assert 'height="1"' in body
    assert body.count("<circle") == 2
    assert '<circle cx="0.5" cy="0.5" r="0.75"/>' in body
    assert '<circle cx="1.5" cy="0.5" r="0.75"/>' in body


def test_dots_svg_rejects_invalid_dimension(client: FlaskClient) -> None:
    response = client.get("/v0/dots?w=0")

    assert response.status_code == 400
