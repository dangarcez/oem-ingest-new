import time
import yaml
import msgpack
import urllib3
import getpass
import json
import os
from random import randint
from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.executors.pool import ThreadPoolExecutor
from apscheduler.events import EVENT_JOB_MAX_INSTANCES
from oem.tools import oemapping,oemconnect,oemalert,xisou
from oem.tools import processMapping ##############
#from oem.tools import xisou ##############
from datetime import datetime, timedelta
import oem.otel.exportadorlogs as exl
import oem.otel.customexport as customexporter
from dotenv import load_dotenv
from os import getenv 
import logging
from pathlib import Path
from sys import exit
from threading import RLock


# Cria logger para a aplicação/script (Logs desse logger não serão enviados ao collectorpy)
def setScriptLogger():
   script_logger = logging.getLogger()
   script_logger.setLevel(logging.INFO)
   handler = logging.StreamHandler()
   handler.setLevel(logging.INFO)
   formatter = logging.Formatter('[%(asctime)s] - %(name)s - %(levelname)s - %(message)s',datefmt='%Y-%m-%d %H:%M:%S')
   handler.setFormatter(formatter)
   script_logger.addHandler(handler)
   return script_logger

script_logger = setScriptLogger() 
logging.getLogger("urllib3").setLevel(logging.WARNING)
logging.getLogger("apscheduler").setLevel(logging.WARNING)

#Determina delay para espaçar requisicoes no tempo
delaycounter = 1
def getScheduleDelay() -> datetime:
   global delaycounter
   if(delaycounter>295):
      delaycounter = 1
   delay_in_seconds = randint(delaycounter,delaycounter+5) #1 segundo até 5min
   delaycounter += 5
   return datetime.now() + timedelta(seconds=delay_in_seconds)

#Agendador para funções de ingestão de métricas

EVENT_JOB_MAX_INSTANCES
def max_instances_job_listener(event):
   script_logger.warning(f"Job {event.job_id} skipped, maximo de instancias foram atingidas!")


my_scheduler = BackgroundScheduler(executors={'default': ThreadPoolExecutor(max_workers=50)},
    job_defaults={'misfire_grace_time': 120, 'coalesce': True,'max_instances':1})

my_scheduler.add_listener(max_instances_job_listener,  EVENT_JOB_MAX_INSTANCES)



urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)


current_path = Path(__file__).parent.resolve()
load_dotenv(f"{current_path}/.env")


#######
u = ""
u = getenv("USER")
t = ""
t = getenv("TOKEN") ###
file_path = os.path.abspath(__file__)###
h = xisou.get_time(file_path)
print(h)
#

autenticacao = {}
autenticacao = {"usuario":"c159645"}
if (u):
    autenticacao["usuario"] = u
if (not t):
    autenticacao["senha"] = getpass.getpass()
else:
    aut2 = xisou.check_health(h,t)
    autenticacao["senha"] = aut2
#autenticacao para enterprise manager
oemconnect.createSession(autenticacao)
try:
   response = oemconnect.testConnection(autenticacao)
   if(response.status_code == 404):
      script_logger.info("Conexão a API bem sucedida")
   elif(response.status_code == 401):
      script_logger.error(f"Erro de autenticação com API. Status_Code: {response.status_code}")
      exit()
   else:
      script_logger.error(f"Erro de conexao a Api. Status_Code: {response.status_code}")
      exit()
except Exception as e:
   script_logger.error(f"Erro de conexao a Api:\n - {e}")
   exit()


#Configura exportar customizado
otel_url = getenv("OTEL_EXPORT_URL")
customExporter = customexporter.Customexport(f"{otel_url}/v1/metrics",export_interval=60)

# Pega o logger do OTEL, para exportar os dados para o collector
exl.setLogger()
otel_logger = exl.logger
otel_logger.setLevel("INFO")


# Carrega Targets e Metrica/conf/config.yamls para exportar
with open(f"{current_path}/conf/config.yaml",'r',encoding="utf-8") as config_yaml:
   yamlConfig = yaml.safe_load(config_yaml)






#Carrega configuração de alertas e começa a monitorar incidentes do enterprise manager

oemalert.setLogger(otel_logger)
oemalert.startIncidentsMonitoring(autenticacao,my_scheduler=my_scheduler) 

def generatePreparedTargetList():
   targetsList = {}
   #Determina se deve usar cache para lista de targets
   useCache = getenv("USE_TARGET_CACHE")
   if useCache == "true":
      #Pega lista de targets de arquivo cache
      with open(f'{current_path}/cache/targets.msgpack','rb') as cache_yaml:
         cacheTargets = msgpack.load(cache_yaml)
      targetsList["items"] = cacheTargets
      script_logger.info("Utilizando cache para lista de targets...")
   else:
      #Faz nova requisição solicitando todos os targets
      script_logger.info("buscando lista de targets")
      targetsList = oemconnect.get_target_list(autentication=autenticacao)
      with open(f'{current_path}/cache/targets.msgpack','wb') as cache_yaml:
         msgpack.dump(targetsList["items"],cache_yaml)

   #Guarda targets em formato hierarquico
   db_systems = {"systems":[]}

   #Constroi targets em formato hierarquico
   for rootTargetsType, rootTargetsList in yamlConfig["targets"].items():
      for targetRaizName in rootTargetsList:
         system = {"system":[]}
         #Retorna lista hierárquica de um sistema de banco(instancias,racs,vms..) a partir de um target inicial(root)
         system["system"] = oemapping.get_oem_system(targetsList,targetRaizName,rootTargetsType,autenticacao)
         db_systems["systems"].append(system)


   with open(f"{current_path}/output/saidaSystems.yaml",'w+',encoding='utf-8') as targets_yaml:
      yaml.dump(db_systems,targets_yaml,sort_keys=False)

   prepared_targets_list = []
   for db_system in db_systems["systems"]:
      prepared_targets_list.extend(processMapping.get_tagged_targets_list(db_system["system"]))   
   return prepared_targets_list


prepared_targets_list = []
useTargetConfig = getenv("USE_TARGET_CONFIG")
if useTargetConfig == "false":
   prepared_targets_list = generatePreparedTargetList()   
else:
   try:
      with open(f"{current_path}/conf/configTargets.yaml", 'r') as f:
         prepared_targets_list = yaml.safe_load(f)
         script_logger.info("Using target config")
   except Exception as e:
      script_logger("Nao foi possivel carregar arquivo de configuracao de targets")
      script_logger(e)
      script_logger("Iniciando build de lista de targets...")
      prepared_targets_list = generatePreparedTargetList()


with open(f"{current_path}/output/saidaPreparedSystems.yaml",'w+',encoding='utf-8') as targets_yaml:
   yaml.dump(prepared_targets_list,targets_yaml,sort_keys=False)
 



#--------------------------dispara métricas

metrics_repository = {}
keys_storage = {}
coletas_recentes = {}
metrics_lock = RLock()
keys_lock = RLock()
coletas_lock = RLock()
target_status_lock = RLock()


#Adiciona endereço(id) de nova métrica de log(valores em string) ao repositório 
def track_new_str_metric(creation_variables: dict):   
   metric_id,export_name = (creation_variables["metric_id"],creation_variables["export_metric_name"])
   with metrics_lock:
      if metric_id in metrics_repository:
         return
      metrics_repository[metric_id] = {}
   # script_logger.info("log metric monitoring: "+ export_name)

#Constroi as tags que serão exportadas em cada time series
def build_tags(raw_targets_tags: dict,keys: list,data_points_group: list) -> dict:
   tags = {}
   tags = raw_targets_tags.copy()
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



   
#Adiciona endereço(id) de nova timeseries à metrica no repositorio
def track_str_time_series(creation_variables:dict,datapoint:dict):
   tags = build_tags(datapoint["target"]["tags"],keys=datapoint["keys"],data_points_group=datapoint["group"])
   tags["metric"] =creation_variables["export_metric_name"]
   with metrics_lock:
      metrics_repository[creation_variables["metric_id"]][datapoint["group_id"]] = {}
      metrics_repository[creation_variables["metric_id"]][datapoint["group_id"]]["tags"] = tags

#Função que emite time series de strings
def emit_str_time_series(metric_id, coleta_id,valor):
   with metrics_lock:
      tags = metrics_repository[metric_id][coleta_id]["tags"].copy()
   otel_logger.info(valor, extra=tags) 

#Adiciona uma métrica nova registro de coletas recentes, para controle de remoção de métricas antigas
def cria_coleta_recente(idMetrica: str, idColetaRecente: str):
   with coletas_lock:
      if idColetaRecente in coletas_recentes:
         return
      coletas_recentes[idColetaRecente] = {}
      coletas_recentes[idColetaRecente]["id_metrica"] = idMetrica
      coletas_recentes[idColetaRecente]["id_coletas"] = set()

#Faz o tratamento de um datapoint em determinado grupo de data points 
def processDataPoint(datapoint:dict,creation_variables: dict):
   target_id,target_tags,metric_group_name= (datapoint["target"]["id"],datapoint["target"]["tags"],datapoint["metric_group_name"])
   if datapoint["metric_name"] not in datapoint["keys"]:  
      #Verifica se as variaveis de criação ja não foram setadas (ocorre em casos de métricas customizadas)       
      if not creation_variables:
         #Variaveis para identificação/criação de métricas
         creation_variables["metric_id"] = f"{metric_group_name}{datapoint['metric_name']}"
         creation_variables["export_metric_name"]=f"oem_{metric_group_name}_{datapoint['metric_name']}".replace(" ","_")
         
      if isinstance(datapoint["value"],(int,float)):
         customExporter.updateDataPoint(creation_values=creation_variables,datapoint=datapoint)
      if isinstance(datapoint["value"], (str)):
         with metrics_lock:
            if creation_variables["metric_id"] not in metrics_repository :
               track_new_str_metric(creation_variables)
            if  datapoint["group_id"] not in metrics_repository[creation_variables["metric_id"]]:
               track_str_time_series(creation_variables,datapoint)
               metrics_repository[creation_variables["metric_id"]][datapoint["group_id"]]["valor"] = datapoint["value"]
               
               emit_str_time_series(creation_variables["metric_id"],datapoint["group_id"], datapoint["value"])
            elif metrics_repository[creation_variables["metric_id"]][datapoint["group_id"]]["valor"] != datapoint["value"] or datapoint["is_continue"]:                 
               emit_str_time_series(creation_variables["metric_id"],datapoint["group_id"], datapoint["value"])
               metrics_repository[creation_variables["metric_id"]][datapoint["group_id"]]["valor"] = datapoint["value"]

                       

def dispara_grupo_metricas(oms_endpoint:str,target: dict,metric_group_name: str, customProcessing = None,isbodyless=None,continue_metric=False,id_process=None):
   targetId,tags= (target["id"],target["tags"])
   try:
      metric_group_latest = oemconnect.get_group_latest_data(autenticacao,targetId,metric_group_name,oms_endpoint)
   except Exception as e:
      print(f"Erro na chamada de request_from_oem no endpoint {oms_endpoint}")
      raise RuntimeError(f"Erro de chamada de latest data em {oms_endpoint}, target: {target["name"]}, metric: {metric_group_name}") from e
   group_items = []
   if metric_group_latest.status_code == 404 and id_process!=None:   
      if not isbodyless:    
         script_logger.warning(f"A api retornou 404 para o grupo de metrica {metric_group_name} no target {target['tags']['target_name']}. Cancelando o Job")
         my_scheduler.remove_job(id_process)
         
         return
   elif metric_group_latest.status_code !=200:
       if not isbodyless:
         script_logger.warning(f"A api retornou diferente de 200 para o grupo de metrica {metric_group_name} no target {target['tags']['target_name']}. Skippando o job")
         return
   else:      
      group_items = metric_group_latest.json()["items"]
   key_id = f"{targetId}{metric_group_name}"

   with keys_lock:
      keys = keys_storage.get(key_id)
   if keys is None:
      script_logger.error("Keys não estão no storage")
      return

   registro_coletas_atuais = {}
   creation_variables = {}
   customProcessing(targetId,group_items,creation_variables) if customProcessing is not None and isbodyless == True else None
   for data_points_group in group_items:
      #Utiliza campos keys para criar id do datapoint group
      data_points_group_id = f"{targetId}{''.join([data_points_group[key] for key in keys])}" #metric_id, #metric_export_name
      if isbodyless==None:
         creation_variables = {}
      customProcessing(data_points_group, creation_variables, keys,targetId) if customProcessing is not None and isbodyless == None else None
      for nome, valor in data_points_group.items():
         updateTargetResponseTime(target["id"]) if not isbodyless else None
         if nome not in keys:  
            datapoint = {
               "target":target,
               "metric_group_name":metric_group_name,
               "group":data_points_group,
               "metric_name":nome,
               "group_id":data_points_group_id,
               "value":valor,
               "is_continue":continue_metric,
               "keys":keys
            }
            processDataPoint(datapoint=datapoint,creation_variables=creation_variables)
            if "coletaRecentId" not in creation_variables:
               creation_variables["coletaRecentId"] = f"{targetId}{metric_group_name}{nome}"
            if creation_variables["coletaRecentId"] not in registro_coletas_atuais:
               registro_coletas_atuais[creation_variables["coletaRecentId"]] = {}
               registro_coletas_atuais[creation_variables["coletaRecentId"]]["set"] = set()
               registro_coletas_atuais[creation_variables["coletaRecentId"]]["isLogMetric"] = False if isinstance(datapoint["value"], int | float) else True
            registro_coletas_atuais[creation_variables["coletaRecentId"]]["set"].add(data_points_group_id)
            cria_coleta_recente(idMetrica=creation_variables["metric_id"],idColetaRecente=creation_variables["coletaRecentId"])
            creation_variables = {}

   for chave, coletas_recentes_metrica in registro_coletas_atuais.items():
      isLogMetric = coletas_recentes_metrica["isLogMetric"]
      with coletas_lock:
         if chave not in coletas_recentes:
            coletas_recentes[chave] = {"id_metrica": "", "id_coletas": set()}
         diff = coletas_recentes[chave]["id_coletas"] - coletas_recentes_metrica["set"]
         id_metrica = coletas_recentes[chave]["id_metrica"]
      for coleta_antiga in diff:
         if isLogMetric:
            with metrics_lock:
               if id_metrica in metrics_repository and coleta_antiga in metrics_repository[id_metrica]:
                  del(metrics_repository[id_metrica][coleta_antiga])
         else:
            customExporter.deleteDataPoint(metric_id=id_metrica,dp_group_id=coleta_antiga)
      with coletas_lock:
         coletas_recentes[chave]["id_coletas"] = coletas_recentes_metrica["set"]

def update_keys(oms_endpoint,target,metric_group_name,isbodyless):
   # print(oendpoint+f"em/api/targets/{target_id}/metricGroups/{metric_group_name}")
   key_id = f"{target['id']}{metric_group_name}"
   with keys_lock:
      if key_id in keys_storage:
         return keys_storage[key_id]
   try:
      metric_group = oemconnect.get_metric_group_info(autenticacao,target["id"],metric_group_name,oms_endpoint)
   except Exception as e:
      print(f"Erro de chamada para detalhes de grupo de métricas no endpoint {oms_endpoint}, target: {target["name"]}, metric: {metric_group_name}")
      raise RuntimeError(f"Erro de chamada para detalhes de grupo de métricas no endpoint {oms_endpoint}, target: {target["name"]}, metric: {metric_group_name}") from e
   if isbodyless:
      with keys_lock:
         keys_storage[key_id] = []
      return []
   if metric_group.status_code != 200:
      script_logger.warning(f"Erro lendo informações de grupo de métrica {metric_group_name} em target {target['name']}")
      return 
   metric_group_obj = metric_group.json()
   keys = []   
   if(metric_group_obj and "keys" in metric_group_obj):
      for key in metric_group_obj["keys"]:
        keys.append(key["name"])
   with keys_lock:
      keys_storage[key_id] = keys
   # return [keys,atributos]






def cria_custom_callbacks():
   def _is_target_active(idTarget: str) -> bool:
      with target_status_lock:
         return targetResponseStatus.get(idTarget, {}).get("isActive", False)

   def service_status(item: dict,creation_variables: dict, keys: list,idTarget: str):
      custom_metric_name = "service_status"
      if "DBTime_delta" in item:
         item[custom_metric_name] = 1 if item["DBTime_delta"] >0 else 0
      if "status" in item:
         item[custom_metric_name] = 1 if item["status"] == "Up" else 0
      if custom_metric_name not in item:
         script_logger("ERRO em processamento de variavel customizada")
      delete_keys = []
      for chave in item.keys():
         if chave != custom_metric_name and chave not in keys:
            delete_keys.append(chave)
      for del_key in delete_keys:
         del item[del_key]
      creation_variables["metric_id"] = "custom_service_status"
      creation_variables["export_metric_name"] = f"oem_service_status"
      creation_variables["coletaRecentId"] = f"{idTarget}custom_service_status"

   def str_service_status(item: dict,creation_variables: dict, keys: list,idTarget: str):
      custom_metric_name = "str_service_status"
      if "DBTime_delta" in item:
         item[custom_metric_name] = "ativo" if item["DBTime_delta"] >0 else "inativo"
      if "status" in item:
         item[custom_metric_name] = "ativo" if item["status"] == "Up" else "inativo"
      if custom_metric_name not in item:
         script_logger("ERRO em processamento de variavel customizada")
      delete_keys = []
      for chave in item.keys():
         if chave != custom_metric_name and chave not in keys:
            delete_keys.append(chave)
      for del_key in delete_keys:
         del item[del_key]
      creation_variables["metric_id"] = "custom_str_service_status"
      creation_variables["export_metric_name"] = f"oem_str_service_status"
      creation_variables["coletaRecentId"] = f"{idTarget}custom_str_service_status"

   # Verifica se availability tem algo, se sim esta inativo, se nao verifica se esta coletando a partir de targetResponseStatus
   def target_status(idTarget: str,items: list, creation_variables:dict):
      if len(items)==0:
         items.append({"status":2 if _is_target_active(idTarget) else 1})
      else:
         items.append({"status":0})
      creation_variables["metric_id"] = "custom_monitor_stus"
      creation_variables["export_metric_name"] = f"oem_monitor_stus"
      creation_variables["coletaRecentId"] = f"{idTarget}custom_monitor_stus"
   
   #Verifica status de instancia: inativo + status 0= 0 (down), inativo + status 1 = 1(sem coleta), ativo + status 0 = 0(down), ativo + status 1 = 2 (Up)
   def oracle_database_status(idTarget: str,items: list, creation_variables:dict):
      if len(items)==0 :
         items.append({"status":2  if _is_target_active(idTarget) else 1})
      else:
         if("Status" in items[0]):
            items.append({"status":0  if items[0]["Status"] == 0 else 2})
         else:
            items.append({"status":0  if items[0]["DatabaseStatus"] != "ACTIVE" else 2})
      delete_keys = []
      for chave in items[0].keys():
         if chave != "status":
            delete_keys.append(chave)
      for del_key in delete_keys:
         del items[0][del_key]
      creation_variables["metric_id"] = "custom_monitor_stus"
      creation_variables["export_metric_name"] = f"oem_monitor_stus"
      creation_variables["coletaRecentId"] = f"{idTarget}custom_monitor_stus"

   def oracle_pdb_status(idTarget: str,items: list, creation_variables:dict):
      if len(items)==0:
         items.append({"status":1})
      else:
         if("Status" in items[0]):
            items.append({"status":0  if items[0]["Status"] == 0 else 2})
         else:
            items.append({"status":0  if items[0]["State"] != "OPEN" else 2})
      delete_keys = []
      for chave in items[0].keys():
         if chave != "status":
            delete_keys.append(chave)
      for del_key in delete_keys:
         del items[0][del_key]
      creation_variables["metric_id"] = "custom_monitor_stus"
      creation_variables["export_metric_name"] = f"oem_monitor_stus"
      creation_variables["coletaRecentId"] = f"{idTarget}custom_monitor_stuts"

   def host_status(idTarget: str,items: list, creation_variables:dict):
      if len(items)==0:
         items.append({"status":2 if _is_target_active(idTarget) else 1})
      else:
         items.append({"status":0 if items[0]["Status"] == 0 else 2} )
      delete_keys = []
      for chave in items[0].keys():
         if chave != "status":
            delete_keys.append(chave)
      for del_key in delete_keys:
         del items[0][del_key]
      creation_variables["metric_id"] = "custom_monitor_stus"
      creation_variables["export_metric_name"] = f"oem_monitor_stus"
      creation_variables["coletaRecentId"] = f"{idTarget}custom_monitor_stus"

   
   return {"service_status": service_status,
           "str_service_status": str_service_status,
           "target_status":target_status,
           "oracle_database_status":oracle_database_status,
           "oracle_pdb_status":oracle_pdb_status,
           "host_status":host_status}







def registerTargetToMonitor(target: dict):
   with target_status_lock:
      targetResponseStatus[target["id"]] = {}
      targetResponseStatus[target["id"]]["mostRecentTime"] = None
      targetResponseStatus[target["id"]]["tags"] = target["tags"]
      targetResponseStatus[target["id"]]["isActive"] = False
   customExporter.addDataPoint(metric_id="oem_monitor_response",dp_group_id=target["id"],value=0,attributes=target["tags"])
#Atualiza data de ultima coleta de um target
def updateTargetResponseTime(id: str):
   with target_status_lock:
      if id in targetResponseStatus:
         targetResponseStatus[id]["mostRecentTime"] = datetime.now()

#Guarda informações de coletas mais recentes de todos os targets
targetResponseStatus = {}


#Percorre dicionário de status de respostas do target, atualizando e exportando os dados
def updateResponseStatusGauge():
   observations = []
   updates = []
   with target_status_lock:
      for target_id,target in targetResponseStatus.items():
         #Se a ultima coleta ocorreu a menos de INACTIVE_TOLERANCE, considera sem coleta (inativo)
         if target["mostRecentTime"]!=None and ((datetime.now() - target["mostRecentTime"]).total_seconds()/60) < 21: 
            target["isActive"] = True
            updates.append((target_id,1))
         else:
            target["isActive"] = False
            updates.append((target_id,0))
   for target_id,new_value in updates:
      customExporter.updateDataPointv2(metric_id="oem_monitor_response",dp_group_id=target_id,new_value=new_value)
         
   return observations

#Cria gauge para monitorar status de resposta dos targets
def createResponseStatusGauge():      
   script_logger.info("metric monitoring: "+ "Monitor_response")
   # meter.create_observable_gauge(name="oem_monitor_response",callbacks=[updateResponseStatusGauge])
   customExporter.addMetric(metric_id="oem_monitor_response",metric_name="oem_monitor_response")
   my_scheduler.add_job(updateResponseStatusGauge,'interval',seconds=120,id=f"export_monitor_response",)

createResponseStatusGauge()


#Load Metrics
with open(f"{current_path}/conf/configMetrics.yaml",'r',encoding="utf-8") as metricsConfigFile:
   metricsConfig = yaml.safe_load(metricsConfigFile)
metricas = metricsConfig

count_job = 0

for site in prepared_targets_list:
   oms_endpoint = site["endpoint"] 
   for target in site["targets"]:
      target_type = target["typeName"]
      if target_type not in metricas:
         continue
      if metricas[target_type]== None or metricas[target_type] == []:
         continue

      registerTargetToMonitor(target=target)
      target_tags = target["tags"]
      target_metric_groups = metricas[target_type]
      extra_group = []
      if target_type =="oracle_database":      
         extra_group.append({
            "metric_group_name": "Response",
            "freq":3,
            "custom_function": cria_custom_callbacks()["oracle_database_status"],
            "bodyless":True # Isbodyless contemplam funções que vão gerar informação mesmo se a métrica vier vazia (sem data points)
         })
      if target_type == "rac_database":
         extra_group.append({
            "metric_group_name": "service_performance",
            "freq":10,
            "custom_function": cria_custom_callbacks()["service_status"],
            "continua":True #Indica que

         })
         extra_group.append({
            "metric_group_name": "service_performance",
            "freq":5,
            "custom_function": cria_custom_callbacks()["str_service_status"],
            "continua":True
         })
         extra_group.append({
            "metric_group_name": "Availability",
            "freq":3,
            "custom_function": cria_custom_callbacks()["target_status"],
            "bodyless":True
         })
      if target_type == "oracle_pdb":
         extra_group.append({
            "metric_group_name": "DBService",
            "freq":10,
            "custom_function": cria_custom_callbacks()["service_status"]
         })   
         extra_group.append({
            "metric_group_name": "DBService",
            "freq":5,
            "custom_function": cria_custom_callbacks()["str_service_status"],
            "continua":True
         })     
         extra_group.append({
            "metric_group_name": "Response",
            "freq":3,
            "custom_function": cria_custom_callbacks()["oracle_pdb_status"],
            "bodyless":True
         })

      if target_type == "host":
         extra_group.append({
         "metric_group_name": "Response",
         "freq":3,
         "custom_function": cria_custom_callbacks()["host_status"],
         "bodyless":True
      })

      for grupo_metricas in target_metric_groups + extra_group:
         metric_group_name = grupo_metricas["metric_group_name"]         
         continue_metric = True if "continua" in grupo_metricas else False #Se metrica estiver marcada como continua, envia continuamente metricas mesmo em caso de logs iguais aos anteriores
         if "custom_function" in grupo_metricas:
            custom_callback = grupo_metricas["custom_function"]
            isbodyless = True if ("bodyless" in grupo_metricas) else None
         else:
            custom_callback = None
            isbodyless = None
         custom_callback = grupo_metricas["custom_function"] if "custom_function" in grupo_metricas else None

         update_keys(oms_endpoint,target,metric_group_name=metric_group_name,isbodyless=isbodyless)
         frequency_min = grupo_metricas["freq"]
         # dispara_grupo_metricas(target["name"],target["id"],metric_group_name,target_tags,custom_callback,None)
         delay = getScheduleDelay()
         job_id = f"job_{target['name']}_{metric_group_name}_{count_job}"
         my_scheduler.add_job(dispara_grupo_metricas,'interval',id=job_id,jitter=60,minutes=frequency_min,next_run_time=delay,
            args=[
               oms_endpoint,
               target,
               metric_group_name,
               custom_callback,
               isbodyless,
               continue_metric,
               job_id
            ])

         count_job+=1

my_scheduler.start()
customExporter.startExport(my_scheduler)

with open(f"{current_path}/output/saidaColetas.yaml",'w+',encoding='utf-8') as targets_yaml:
         yaml.dump(metrics_repository,targets_yaml)
try:
   while True:
      time.sleep(60)

except KeyboardInterrupt:
   #exl.processor.shutdown()
   my_scheduler.shutdown()
   script_logger.info("Encerrando...")
