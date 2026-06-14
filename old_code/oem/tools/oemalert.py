import operator
import logging
import re
import yaml
import oem.otel.exportadorlogs as exl
import oem.tools.oemconnect as oemconnect
import time
from apscheduler.schedulers.background import BackgroundScheduler
from datetime import datetime, timedelta, timezone
from random import randint
import requests

def getScheduleDelay() -> datetime:
   delay_in_seconds = randint(1,300) #1 segundo até 5min
   return datetime.now() + timedelta(seconds=delay_in_seconds)

    
def convertTime(tempo: str): #Tambem corrige bug que api está a frente 3horas

   dt = datetime.strptime(tempo, "%Y-%m-%dT%H:%M:%S.%fZ")
   dt_minus_3 = dt - timedelta(hours=3)
   new_iso_string = dt_minus_3.strftime("%Y-%m-%dT%H:%M:%S.%f")[:-3] + "Z"
   return new_iso_string



logger = logging.getLogger(__name__)

alertas = {}
incidentsList = set()
monitoringStarted = False
scheduler = None
logger = None
def setLogger(load_logger: logging.Logger):
   global logger
   logger = load_logger


def startIncidentsMonitoring(autentication: dict,my_scheduler: BackgroundScheduler):
   global scheduler
   global monitoringStarted
   scheduler = my_scheduler
   monitoringStarted = True
   scheduler.add_job(getNewIncidents,'interval',id=f"incident_job",minutes=5,args=[autentication])
   getNewIncidents(autentication=autentication)

def checkIncidentStatus(autentication: dict,id: str,id_process: str,mensagem: str):
   incident = oemconnect.get_incident(autentication=autentication,id=id)
   status = incident.status_code
   incident = incident.json()
   if status!=200 or incident["status"] == "Closed":
      scheduler.remove_job(id_process)
      incidentsList.remove(id)
      return
   del incident["message"]


def getNewIncidents(autentication: dict):
   incidents = oemconnect.get_incident_list(autentication=autentication,timeWindowInHours=1)
   for incident in incidents["items"]:
      if incident["id"] in incidentsList:
         continue
      incident["timeCreated"] = convertTime(incident["timeCreated"])
      incident["timeUpdated"] = convertTime(incident["timeUpdated"])

      mensagem = incident["message"]
      del incident["message"]
      logger.warning(mensagem,extra=incident)
      scheduler.add_job(checkIncidentStatus,'interval',id=f"incident_{incident['id']}",minutes=60,args=[autentication,incident["id"],f"incident_{incident['id']}",mensagem])
      incidentsList.add(incident["id"])
