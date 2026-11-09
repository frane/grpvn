"""Download public release assets; verification remains a separate init step."""
import json
import os
import shutil
import time
import urllib.error
import urllib.request


def download(url, destination, accept="application/octet-stream"):
    for attempt in range(3):
        try:
            request = urllib.request.Request(url, headers={"Accept": accept, "User-Agent": "grpvn-release-installer"})
            with urllib.request.urlopen(request, timeout=60) as response, open(destination + ".part", "wb") as output:
                shutil.copyfileobj(response, output, length=1024 * 1024)
            os.replace(destination + ".part", destination)
            return
        except (urllib.error.URLError, TimeoutError, ConnectionError) as error:
            if attempt == 2 or isinstance(error, urllib.error.HTTPError) and error.code < 500 and error.code != 429:
                raise
            time.sleep(2 * (attempt + 1))


if __name__ == "__main__":
    version = os.environ["VERSION"]
    asset = f"grpvn_{version}_linux_x86_64.tar.gz"
    download(f"https://api.github.com/repos/frane/grpvn/releases/tags/v{version}", "release.json", "application/vnd.github+json")
    with open("release.json", encoding="utf-8") as metadata:
        assets = {item["name"]: item["url"] for item in json.load(metadata)["assets"]}
    for name in (asset, "checksums.txt"):
        download(assets[name], name)
    print("Release assets downloaded; checksum verification follows.", flush=True)
