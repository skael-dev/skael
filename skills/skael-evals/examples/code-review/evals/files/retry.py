import time

import requests


def fetch_with_retry(url):
    while True:
        try:
            resp = requests.get(url)
            return resp.json()
        except:
            print("retrying")
            continue
