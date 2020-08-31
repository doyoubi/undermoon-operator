FROM python:3.8.3-buster

WORKDIR /checker

COPY ./chaostest/requirements.txt /checker/
COPY ./chaostest/cluster_client.py /checker/
COPY ./chaostest/check_service.py /checker/
COPY ./chaostest/chaos_check_service.py /checker/

RUN pip install -r requirements.txt
