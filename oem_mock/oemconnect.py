import json
import requests
from requests.auth import HTTPBasicAuth
from requests.models import Response




s = None
DEFAULT_LIMIT = 200
def createSession(autentication: dict):
    global s
    s = requests.Session()
    s.auth = (autentication["usuario"],autentication["senha"]) 

def request_from_oem(autentication: dict,url_path : str):
   oendpoint = 'http://localhost:8000' #################
   response =s.get(
       oendpoint+url_path,
       verify=False
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

def get_metric_group_info(autentication: dict, targetId: str,group_name: str):
    response = request_from_oem(autentication,f"/em/api/targets/{targetId}/metricGroups/{group_name}")
    return response


def get_group_latest_data(autentication: dict,targetId: str, group_name: str):
    url_path = _append_limit(f"/em/api/targets/{targetId}/metricGroups/{group_name}/latestData", DEFAULT_LIMIT)
    response = request_from_oem(autentication, url_path)
    if response.status_code != 200:
       return response

    data = response.json()
    nextLink = data.get("links", {}).get("next", {}).get("href")
    while nextLink is not None:
       next_resp = request_from_oem(autentication, nextLink)
       if next_resp.status_code != 200:
          break
       next_data = next_resp.json()
       data["items"].extend(next_data.get("items", []))
       nextLink = next_data.get("links", {}).get("next", {}).get("href")

    if "links" in data and "next" in data["links"]:
       del data["links"]["next"]
    return _build_json_response(data, response.status_code, url=response.url)

def get_metric_latest_data(autentication: dict,targetId: str, group_name: str,metric_name: str):
    response = request_from_oem(autentication,f"/em/api/targets/{targetId}/metricGroups/{group_name}/latestData?metricName={metric_name}")
    return response

def get_target_properties(autentication: dict, targetId: str):
    response = request_from_oem(autentication, f"/em/api/targets/{targetId}/properties")
    return response

def get_custom_call(autentication: dict, url: str):
    response = request_from_oem(autentication=autentication,url_path=url)
