# !/usr/bin/env python3
import argparse
import logging
import os
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument("-upx", action="store_true")
parser.add_argument("-i", type=int)
args = parser.parse_args()

PROJECT_NAME = 'mosdns'
RELEASE_DIR = './release'

logger = logging.getLogger(__name__)


def go_build():
    logger.info(f'building {PROJECT_NAME}')

    VERSION = 'dev/unknown'
    try:
        VERSION = subprocess.check_output('git describe --tags --long --always', shell=True).decode().rstrip()
    except subprocess.CalledProcessError as e:
        logger.error(f'get git tag failed: {e.args}')

    bin_filename = PROJECT_NAME
    
    logger.info(f'building {bin_filename}')
    try:
        subprocess.check_call(
            f'go build -ldflags "-s -w -X main.version={VERSION}" -trimpath -o {bin_filename} -pgo=auto ../', shell=True,
            env={"GOOS": "linux", "GOARCH": "amd64", "GOAMD64": "v3", **os.environ})

        if args.upx:
            try:
                subprocess.check_call(f'upx -9 -q {bin_filename}', shell=True, stderr=subprocess.DEVNULL,
                                        stdout=subprocess.DEVNULL)
            except subprocess.CalledProcessError as e:
                logger.error(f'upx failed: {e.args}')
    except Exception:
        logger.exception('unknown err')


if __name__ == '__main__':
    logging.basicConfig(level=logging.INFO)

    if len(RELEASE_DIR) != 0:
        if not os.path.exists(RELEASE_DIR):
            os.mkdir(RELEASE_DIR)
        os.chdir(RELEASE_DIR)

    go_build()
