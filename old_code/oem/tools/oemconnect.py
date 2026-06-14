import json
import requests
from requests.auth import HTTPBasicAuth
from requests.adapters import HTTPAdapter
from requests.models import Response
from urllib3.util.retry import Retry
from os import getenv 
from dotenv import load_dotenv


sessao = None
DEFAULT_TIMEOUT = (5, 35) # 5 = Connect timeout (Resolver DNS, abrir socket tcp, handshake inicial), 30 = read timeout (É o tempo máximo sem receber bytes do servidor.)
DEFAULT_LIMIT = 200


def createSession(autentication: dict):
    global protocol
    global em_url
    global sessao
    protocol = getenv("PROTOCOL")
    em_url = getenv("EM_BASE_URL")
    sessao = requests.Session()
    sessao.auth = (autentication["usuario"],autentication["senha"]) 
    retry = Retry(
        total=3, #Retenta 3 vezes caso receba um dos retornos em "status_forcelist"
        backoff_factor=0.5, # Fator de tempo entre retrys >sleep = backoff_factor * (2 ** tentativa).Ex: 0,5s | 1s | 2s
        status_forcelist=(429, 500, 502, 503, 504), # retornos para qual fazer tentativas 
        allowed_methods=("GET",), # Metodo suportado (POST/PUT/PATCH podem criar dados duplicados)
        raise_on_status=False, # True = lança exceção no caso de todas os retrys falharem. False mantem fluxo padrão (igual se não tivesse retry)
    )
    adapter = HTTPAdapter(pool_connections=10,pool_maxsize=50,pool_block=False,max_retries=retry)
    sessao.mount("http://",adapter=adapter)
    sessao.mount("https://",adapter=adapter)
    print(DEFAULT_TIMEOUT, flush=True)


def request_from_oem(autentication: dict,url_path : str, oms_endpoint:str=None, timeout=DEFAULT_TIMEOUT):
   global protocol, em_url, reqCount, reqVolumeKBytes
   oendpoint = oms_endpoint if oms_endpoint else f'{protocol}://{em_url}' #################
   response = sessao.get(
      oendpoint+url_path,
      verify=False,
      timeout=timeout
   )
   return response

def _append_limit(url_path: str, limit: int) -> str:
   if "?" in url_path:
      return f"{url_path}&limit={limit}"
   return f"{url_path}?limit={limit}"

def _build_json_response(payload: dict, status_code: int, url: str | None = None) -> Response:
   response = Response()
   response.status_code = status_code
   response._content = json.dumps(payload).encode("utf-8")
   response.headers["Content-Type"] = "application/json"
   if url:
      response.url = url
   return response

def testConnection(autentication: dict):
    response = request_from_oem(autentication, f"/em/api")
    return response

def get_incident(autentication: dict, id: str):
    response = request_from_oem(autentication,f"/em/api/incidents/{id}")
    return response

def get_incident_list(autentication: dict,timeWindowInHours: int) -> list:
    incidents = request_from_oem(autentication,f"/em/api/incidents/?ageInHoursLessThanOrEqualTo={timeWindowInHours}")
    incidents = incidents.json()
    nextLink = None
    # if "next" in incidents["links"]:
    #     nextLink = incidents["links"]["next"]["href"]
    # while nextLink is not None:
    #     newincidents = request_from_oem(autentication,nextLink)
    #     newincidents = newincidents.json()
    #     incidents["items"].extend(newincidents["items"])
    #     if "next" in newincidents["links"]:           
    #         nextLink = newincidents["links"]["next"]["href"]
    #         # print(nextLink)
    #     else:
    #         nextLink = None
    return incidents

def get_target_list(autentication: dict) -> list:  
   targets = request_from_oem(autentication,"/em/api/targets")
   targets = targets.json()
   nextLink = None
   if "next" in targets["links"]:
       nextLink = targets["links"]["next"]["href"]
   while nextLink is not None:
       newtargets = request_from_oem(autentication,nextLink)
       newtargets = newtargets.json()
       targets["items"].extend(newtargets["items"])
       if "next" in newtargets["links"]:           
           nextLink = newtargets["links"]["next"]["href"]
           # print(nextLink)
       else:
           nextLink = None
   return targets

def get_metric_group_info(autentication: dict, targetId: str,group_name: str,oms_endpoint:str=None):
    response = request_from_oem(autentication,f"/em/api/targets/{targetId}/metricGroups/{group_name}",oms_endpoint=oms_endpoint)
    return response


def get_group_latest_data(autentication: dict,targetId: str, group_name: str,oms_endpoint:str=None):
    url_path = _append_limit(f"/em/api/targets/{targetId}/metricGroups/{group_name}/latestData", DEFAULT_LIMIT)
    response = request_from_oem(autentication, url_path, oms_endpoint=oms_endpoint)
    if response.status_code != 200:
        return response

    data = response.json()
    nextLink = data.get("links", {}).get("next", {}).get("href")
    while nextLink is not None:
        next_resp = request_from_oem(autentication, nextLink, oms_endpoint=oms_endpoint)
        if next_resp.status_code != 200:
            break
        next_data = next_resp.json()
        data["items"].extend(next_data.get("items", []))
        nextLink = next_data.get("links", {}).get("next", {}).get("href")

    if "links" in data and "next" in data["links"]:
        del data["links"]["next"]
    return _build_json_response(data, response.status_code, url=response.url)

def get_metric_latest_data(oms_endpoint:str,autentication: dict,targetId: str, group_name: str,metric_name: str):
    response = request_from_oem(autentication,f"/em/api/targets/{targetId}/metricGroups/{group_name}/latestData?metricName={metric_name}",oms_endpoint=oms_endpoint)
    return response

def get_target_properties(autentication: dict, targetId: str):
    response = request_from_oem(autentication, f"/em/api/targets/{targetId}/properties")
    return response

def get_custom_call(autentication: dict, url: str):
    response = request_from_oem(autentication=autentication,url_path=url)
