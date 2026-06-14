##################

FROM python:3.12-slim-bookworm
#RUN apk add --no-cache bash
#ENV PIP_NO_CACHE_DIR=1 \
#    PIP_EXTRA_INDEX_URL=http://binario.caixa:8081/repository/pypi-repo/simple/
WORKDIR /app
COPY requirements.txt .
#RUN pip config --user set global.index-url http://binario.caixa:8081/repository/pypi-repo/simple/
#RUN pip config --user set global.index http://binario.caixa:8081/repository/pypi-repo/pypi/
#RUN pip config --user set global.trusted-host binario.caixa
RUN pip install -r requirements.txt
COPY . .
CMD ["python3","script.py"]
####################
