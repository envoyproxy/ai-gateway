# Copyright Envoy AI Gateway Authors
# SPDX-License-Identifier: Apache-2.0
# The full text of the Apache license is available in the LICENSE file at
# the root of the repo.

# Use glibc-based image with pre-compiled wheels for psutil
FROM python:3.13-slim

RUN python -m pip install --upgrade pip

COPY requirements.txt /tmp
RUN pip install -r /tmp/requirements.txt

# Copy the proxy server
COPY openai_proxy.py /app/

WORKDIR /app

# Expose the proxy server port
EXPOSE 8080

# Set Python to run unbuffered for real-time logging
ENV PYTHONUNBUFFERED=1

# Run the proxy server
ENTRYPOINT ["python", "-u", "openai_proxy.py"]
