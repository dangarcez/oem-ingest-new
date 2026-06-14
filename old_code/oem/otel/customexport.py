import copy
import logging
from os import getenv
from threading import RLock
import time

import requests
from apscheduler.schedulers.background import BackgroundScheduler
from requests.exceptions import ConnectionError

from oem.tools import schema_pb2

# Configuração básica do logging
logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)


class Customexport:
    def __init__(self, collector_url:str,export_interval:int=120):
        self._collector_url = collector_url
        self._session = requests.Session()
        self._session.headers.update({"Content-Type":"application/x-protobuf"})
        self._exportInterval = export_interval
        self._timeout = (5, 30)
        self._lock = RLock()
        self._profile_export = getenv("EXPORT_PROFILE") == "true"
        self._metrics_repository = {}
        self._service_name = "oemAPIService"
        self._scope_name = "oem.metrics.collector"

    def startExport(self,scheduler: BackgroundScheduler):
        logger.info('start_exporting')
        scheduler.add_job(self._exportPayload,'interval',id=f"export_metrics_job",seconds=self._exportInterval)

    def _exportPayload(self):
        if self._profile_export:
            t0 = time.perf_counter()
            self._lock.acquire()
            t_lock = time.perf_counter()
            cpu_lock = time.process_time()
            try:
                repository_snapshot = copy.deepcopy(self._metrics_repository)
            finally:
                self._lock.release()
            t1 = time.perf_counter()
            cpu1 = time.process_time()
            payload_snapshot = self._buildPayload(repository_snapshot, time.time_ns())
            t_build = time.perf_counter()
            cpu_build = time.process_time()
            serialized_payload = payload_snapshot.SerializeToString()
            t2 = time.perf_counter()
            cpu2 = time.process_time()
            metric_count, datapoint_count = self._countRepositoryEntries(repository_snapshot)
            logger.info(
                (
                    "export_profile: lock_wait_ms=%.2f deepcopy_ms=%.2f deepcopy_cpu_ms=%.2f "
                    "build_ms=%.2f build_cpu_ms=%.2f serialize_ms=%.2f "
                    "serialize_cpu_ms=%.2f metric_count=%d datapoint_count=%d payload_bytes=%d"
                ),
                (t_lock - t0) * 1000,
                (t1 - t_lock) * 1000,
                (cpu1 - cpu_lock) * 1000,
                (t_build - t1) * 1000,
                (cpu_build - cpu1) * 1000,
                (t2 - t_build) * 1000,
                (cpu2 - cpu_build) * 1000,
                metric_count,
                datapoint_count,
                len(serialized_payload),
            )
        else:
            with self._lock:
                repository_snapshot = copy.deepcopy(self._metrics_repository)
            payload_snapshot = self._buildPayload(repository_snapshot, time.time_ns())
            serialized_payload = payload_snapshot.SerializeToString()
        try:
            if self._profile_export:
                t_post0 = time.perf_counter()
                cpu_post0 = time.process_time()
            resp = self._session.post(self._collector_url, data=serialized_payload, timeout=self._timeout)
            if self._profile_export:
                t_post1 = time.perf_counter()
                cpu_post1 = time.process_time()
                elapsed_time_seconds = resp.elapsed.total_seconds()
                logger.info(
                    (
                        "export_profile: export_ms=%.2f export_cpu_ms=%.2f "
                        "response_elapsed_ms=%.2f status_code=%s"
                    ),
                    (t_post1 - t_post0) * 1000,
                    (cpu_post1 - cpu_post0) * 1000,
                    elapsed_time_seconds * 1000,
                    getattr(resp, "status_code", "unknown"),
                )
        except ConnectionError as e:
            logger.error(f"Erro de Conexao com otel collector: {e}")
        except Exception as e :
            logger.error(f"Erro ao fazer post request em otel collector: {e}")
        # print(f"[{time.strftime('%X')}] Status: {resp.status_code} {resp.text}",flush=True)

    def addMetric(self,metric_id:str,metric_name):
        with self._lock:
            if(metric_id in self._metrics_repository):
                return
            self._metrics_repository[metric_id] = self._createMetric(metric_name)

    def addDataPoint(self, metric_id:str,dp_group_id:str,value: int | float, attributes:dict):
        with self._lock:
            if(metric_id not in self._metrics_repository):
                print(f"Tentativa de adicionar datapoint ({dp_group_id}) em metrica que nao existe na repo ({metric_id})")
                return
            metric = self._metrics_repository[metric_id]
            if(dp_group_id in metric["datapoints"]):
                return 
            metric["datapoints"][dp_group_id] = self._createDatapoint(value, attributes)

    def _buildAttributes(self,datapoint):
        tags = {}
        tags = datapoint["target"]["tags"].copy()
        keys = datapoint["keys"]
        data_points_group = datapoint["group"]
        #adiciona keys do grupo às tags
        for key in keys:
            tags[key] = data_points_group[key]
        #retira tags que geram conflito de tags do otel/prometheus
        if "instance" in tags:
                tags["_instance"] = tags["instance"]
                del tags["instance"]
        if "service_name" in tags:
                tags["name"] = tags["service_name"]
                del tags["service_name"]   
        #trata conflito com name dos logs
        if "name" in tags:
                tags["name_"] = tags["name"]
                del tags["name"]   
        #trata tag para facilitar trabalhar com as labels no grafana
        if "Username_machine" in tags:
            lista1 = tags["Username_machine"].split('_')
            tags["user"] = lista1[0]
            tags["pod"] = lista1[1]
        return tags

    def updateDataPoint(self,creation_values,datapoint):
        metric_id = creation_values["metric_id"]
        dp_group_id = datapoint["group_id"]
        new_value = datapoint["value"]
        with self._lock:
            if(metric_id not in self._metrics_repository):
                self.addMetric(metric_id=metric_id,metric_name=creation_values["export_metric_name"])
               #  logger.info("metric monitoring: "+ creation_values["export_metric_name"])
            metric = self._metrics_repository[metric_id]
            if(dp_group_id  not in metric["datapoints"]):
                attributes = self._buildAttributes(datapoint)
                self.addDataPoint(metric_id=metric_id,dp_group_id=dp_group_id ,value=datapoint["value"],attributes=attributes)
                return
            metric["datapoints"][dp_group_id]["value"] = new_value

    def updateDataPointv2(self,metric_id:str,dp_group_id:str,new_value:int | float):
        with self._lock:
            if(metric_id not in self._metrics_repository):
                print(f"Tentativa de atualizar datapoint ({dp_group_id}) em metrica que nao existe na repo ({metric_id})")
                return
            metric = self._metrics_repository[metric_id]
            if(dp_group_id not in metric["datapoints"]):
                print(f"Tentativa de atualizar datapoint ({dp_group_id}) que nao existe na metrica ({metric_id})")
                return
            metric["datapoints"][dp_group_id]["value"] = new_value
    
    def deleteDataPoint(self,metric_id:str,dp_group_id:str):
        with self._lock:
            if(metric_id not in self._metrics_repository):
                print(f"Tentativa de deletar datapoint ({dp_group_id}) em metrica que nao existe na repo ({metric_id})")
                return
            metric = self._metrics_repository[metric_id]
            if(dp_group_id not in metric["datapoints"]):
                print(f"Tentativa de deletar datapoint ({dp_group_id}) que nao existe na metrica ({metric_id})")
                return
            del metric["datapoints"][dp_group_id]
            if not metric["datapoints"]:
                del self._metrics_repository[metric_id]

    def _createMetric(self,name:str,unit="",description:str =""):
        return {
            "name": name.lower(),
            "unit": unit,
            "description": description,
            "datapoints": {},
        }

    def _createDatapoint(self, value: int | float, attributes: dict | None = None):
        return {
            "value": value,
            "attributes": dict(attributes or {}),
        }

    def _buildPayload(
        self,
        repository_snapshot: dict,
        export_time_unix_nano: int,
    ) -> schema_pb2.ExportMetricsServiceRequest:
        payload = schema_pb2.ExportMetricsServiceRequest()
        resource_metrics = payload.resource_metrics.add()
        service_name = resource_metrics.resource.attributes.add()
        service_name.key = "service.name"
        service_name.value.string_value = self._service_name
        scope_metrics = resource_metrics.scope_metrics.add()
        scope_metrics.scope.name = self._scope_name

        for metric_data in repository_snapshot.values():
            metric = scope_metrics.metrics.add()
            metric.name = metric_data["name"]
            metric.description = metric_data["description"]
            metric.unit = metric_data["unit"]

            for datapoint_data in metric_data["datapoints"].values():
                datapoint = metric.gauge.data_points.add()
                self._populateDatapoint(
                    datapoint,
                    value=datapoint_data["value"],
                    attributes=datapoint_data["attributes"],
                    timeUnix=export_time_unix_nano,
                )

        return payload

    def _countRepositoryEntries(self, repository_snapshot: dict) -> tuple[int, int]:
        metric_count = len(repository_snapshot)
        datapoint_count = sum(
            len(metric_data["datapoints"])
            for metric_data in repository_snapshot.values()
        )
        return metric_count, datapoint_count
    
    def _populateDatapoint(
        self,
        datapoint: schema_pb2.NumberDataPoint,
        value: int | float = 0,
        attributes: dict = None,
        timeUnix: int = None,
    ):
        datapoint.time_unix_nano = int(timeUnix) if timeUnix is not None else int(time.time() * 1e9)

        if attributes:
            for chave,valor in attributes.items():
                attribute = datapoint.attributes.add()
                attribute.key = str(chave)
                self._setAnyValue(attribute.value, valor)

        self._setDatapointValue(datapoint, value)

    def _setAnyValue(self, any_value: schema_pb2.AnyValue, value):
        if isinstance(value, bool):
            any_value.bool_value = value
        elif isinstance(value, int):
            any_value.int_value = value
        elif isinstance(value, float):
            any_value.double_value = value
        elif isinstance(value, (bytes, bytearray)):
            any_value.bytes_value = bytes(value)
        else:
            any_value.string_value = str(value)

    def _setDatapointValue(self, datapoint: schema_pb2.NumberDataPoint, value: int | float):
        current_type = datapoint.WhichOneof("value")
        if current_type == "as_double" or isinstance(value, float):
            datapoint.as_double = float(value)
            return
        datapoint.as_int = int(value)
