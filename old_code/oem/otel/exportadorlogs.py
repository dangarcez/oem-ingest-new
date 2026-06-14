from opentelemetry.sdk._logs import LoggerProvider, LoggingHandler
from opentelemetry.sdk._logs.export import BatchLogRecordProcessor, ConsoleLogExporter
from opentelemetry.sdk._logs.export import ConsoleLogExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.exporter.otlp.proto.http._log_exporter import OTLPLogExporter
from opentelemetry._logs import set_logger_provider, get_logger
from opentelemetry._logs.severity import SeverityNumber
from opentelemetry.sdk._logs import LogRecord
from datetime import datetime
import logging
from os import getenv
import time

# Configuração básica do logging
lib_logger = logging.getLogger(__name__)
lib_logger.setLevel(logging.INFO)
logger = None
def setLogger():
    global logger
    # Configura o logger provider
    resource = Resource({
       "service.name":"oemAPIService",
    })
    logger_provider = LoggerProvider(resource=resource)


    set_logger_provider(logger_provider)

    
    otel_url = getenv("OTEL_EXPORT_URL")
    otel_to_console = getenv("OTEL_TO_CONSOLE")
    if (otel_to_console=="true"):
        lib_logger.info("Exportando logs para console")
        log_otlp_exporter = ConsoleLogExporter()
    else:
        lib_logger.info("Exportando logs para colector")
        log_otlp_exporter = OTLPLogExporter(endpoint=f"{otel_url}/v1/logs")     
    processor = BatchLogRecordProcessor(log_otlp_exporter, schedule_delay_millis=100_000)
    logger_provider.add_log_record_processor(processor)

    # handler = LoggingHandler(logger_provider=logger_provider)
    # logging.getLogger().addHandler(handler)
    # logging.getLogger().setLevel(logging.INFO) # Set desired logging level

    handler = LoggingHandler(level=logging.NOTSET,logger_provider=logger_provider)
    logger = logging.getLogger(__name__)
    logger.addHandler(handler)
    logger.propagate = False
