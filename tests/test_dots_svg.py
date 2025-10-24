from fastapi.testclient import TestClient


def test_dots_svg_defaults(client: TestClient) -> None:
    response = client.get("/v0/dots")

    assert response.status_code == 200
    assert response.headers["content-type"] == "image/svg+xml"

    body = response.text
    assert 'width="64"' in body
    assert 'height="32"' in body
    assert body.count("<circle") == 64 * 32
    assert '<circle cx="0.5" cy="0.5" r="0.3"/>' in body


def test_dots_svg_custom_parameters(client: TestClient) -> None:
    response = client.get("/v0/dots?w=2&h=1&r=0.75")

    assert response.status_code == 200

    body = response.text
    assert 'width="2"' in body
    assert 'height="1"' in body
    assert body.count("<circle") == 2
    assert '<circle cx="0.5" cy="0.5" r="0.75"/>' in body
    assert '<circle cx="1.5" cy="0.5" r="0.75"/>' in body


def test_dots_svg_rejects_invalid_dimension(client: TestClient) -> None:
    response = client.get("/v0/dots?w=0")

    assert response.status_code == 400
