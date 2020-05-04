#!/bin/bash 

DEVICE_TYPE="raspberrypi3"
ARTIFACT_NAME="$1.img"                                                           
RAW_DISK_IMAGE="input/$1.img"                                                    
MENDER_DISK_IMAGE="$1.sdimg"

./docker-mender-convert from-raw-disk-image  \
    --raw-disk-image $RAW_DISK_IMAGE \
    --mender-disk-image $MENDER_DISK_IMAGE \
    --device-type $DEVICE_TYPE  \
    --artifact-name $ARTIFACT_NAME \
    --bootloader-toolchain arm-buildroot-linux-gnueabihf \
    --storage-total-size-mb 8000
    