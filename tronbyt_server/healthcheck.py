import sys
import requests


def health_check(url: str) -> bool:
    try:
        response = requests.get(url)
        if response.status_code == 200:
            return True
        else:
            return False
    except requests.exceptions.RequestException as e:
        print(f"Failed: {e}")
        return False


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("Usage: python -m tronbyt_server.healthcheck <URL>", file=sys.stderr)
        sys.exit(1)

    url: str = sys.argv[1]
    result: bool = health_check(url)

    sys.exit(0 if result else 1)
