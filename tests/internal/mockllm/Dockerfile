# Copyright Envoy AI Gateway Authors
# SPDX-License-Identifier: Apache-2.0
# The full text of the Apache license is available in the LICENSE file at
# the root of the repo.

FROM python:3.13-slim

RUN python -m pip install --upgrade pip

COPY requirements.txt /tmp/
RUN pip install -r /tmp/requirements.txt

COPY mock_llm.py /app/

WORKDIR /app

ENV PYTHONUNBUFFERED=1

ENTRYPOINT ["python", "-u", "mock_llm.py"]
