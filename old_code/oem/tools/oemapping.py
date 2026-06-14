import re
import oem.tools.oemconnect as oemconnect
PDB = "PDB"
RAC = "RAC"


def searchDictList(lista,chave: str,regex_pattern: re.Pattern,lazySearch: bool):
   if lazySearch:

       resultado = next((item for item in lista if regex_pattern.fullmatch(item[chave])), None)
       return [resultado] if resultado!=None else []
   return [item for item in lista if regex_pattern.fullmatch(item[chave])]

def get_oem_target(targetList: list,targetName: str):
    result = searchDictList(targetList["items"],"name", re.compile(targetName),lazySearch=True)
    if result!=[]:
       return result[0]
    return None ########### SUBSTITUIR POR UM ESQUEMA QUE PEGA OS TARGETS EM PARALELO JA NO GET_OEM_SYSTEM




def get_oem_system(targetList: list,rootName: str,rootType: str,autentication: dict) -> list:
    def get_profundidade(head: dict,count=0):
        if "children" in head:
            return get_profundidade(head["children"],count+1)
        return count
    def get_target_info(regex_pattern_options: re.Pattern,typeName: str,isUnique: bool):
        targets = []
        for regex_pattern in regex_pattern_options:
            targets = searchDictList(targetList["items"],"name",regex_pattern,lazySearch=isUnique)
            if len(targets) > 0:
                break

        if len(targets)>0:
            targets_relatorio = []
            for target in targets:
                target_resumido = {"id":target["id"],"name":target["name"],"typeName":typeName}
                target_resumido["children"] = []

                response = oemconnect.get_target_properties(autentication,target_resumido["id"])
                if response.status_code==200:
                    properties = response.json()
                    if target_resumido["typeName"] != "rac_database" and target_resumido["typeName"] == "oracle_database":
                        dataGuardStatus = searchDictList(properties["items"],"id",re.compile(rf"DataGuardStatus"),lazySearch=True)
                        target_resumido["dg_role"] = dataGuardStatus[0]["value"] if len(dataGuardStatus) > 0 else "unknown"
                    if target_resumido["typeName"] == "oracle_database":
                        #get hosts and listeners
                        hostname = searchDictList(properties["items"],"id",re.compile(rf"MachineName"),lazySearch=True)[0]["value"]
                        hostname = hostname.replace("-vip","")
                        machines = get_target_info([re.compile(hostname)],typeName="host",isUnique = True)
                        listeners = get_target_info([re.compile(f'LISTENER_{hostname}')],typeName="oracle_listener",isUnique = True)
                        if(len(machines)>0):
                            machines[0]["name"] = hostname
                            machines[0]["children"] = []
                            target_resumido["machine_name"] = machines[0]["name"]
                        if(len(listeners)>0):
                            listeners[0]["name"] = f"LISTENER_{hostname}"
                            listeners[0]["children"] = []
                            target_resumido["listener_name"] = listeners[0]["name"]
                        target_resumido["children"].extend(machines + listeners)
                targets_relatorio.append(target_resumido)
            return targets_relatorio
        return []

    system = []
    primSearchList = []
    stbySearchList = []
    isPdb  = True if rootType == PDB else False
    rac_database_name = re.match(r'([^_]+)_', rootName).group(1) if isPdb else rootName
    rac_database = {"reg_pattern_list": [re.compile(rf"{rac_database_name}")], "targetType": "rac_database","unique": True}
    rac_database_name = rac_database_name.split("_")[0]

    rac_database_stby = {"reg_pattern_list": [re.compile(rf"{rac_database_name.replace('p','s')}"),re.compile(rf"{rac_database_name.replace('p','s')}_1")], "targetType": "rac_database","unique": True}
    oracle_dbsys = {"reg_pattern_list" : [re.compile(rf"{rac_database_name}_sys")],"targetType": "oracle_dbsys","unique" : True}
    oracle_dbsys_stby = {"reg_pattern_list" : [re.compile(rf"{rac_database_name.replace('p','s')}_sys")],"targetType": "oracle_dbsys","unique" : True}
    oracle_database ={"reg_pattern_list":[re.compile(rf"^{rac_database_name}(?:_\d+)?_{rac_database_name}\d*$")],"targetType":"oracle_database","unique":False}
    oracle_database_stby = {"reg_pattern_list":[re.compile(rf"^{rac_database_name.replace('p','s')}(?:_\d+)?_{rac_database_name.replace('p','s')}\d*$")],"targetType":"oracle_database","unique":False}
    primSearchList.extend([oracle_dbsys,rac_database,oracle_database])
    stbySearchList.extend([oracle_dbsys_stby,rac_database_stby,oracle_database_stby])
    if isPdb:
        oracle_pdb={"reg_pattern_list": [re.compile(rf"{rootName}")], "targetType":"oracle_pdb","unique":True}
        oracle_pdb_stby={"reg_pattern_list": [re.compile(rf"{rootName.replace('p','s')}")], "targetType":"oracle_pdb","unique":True}
        primSearchList.insert(2,oracle_pdb)
        stbySearchList.insert(2,oracle_pdb_stby)
    test_root = searchDictList(targetList["items"],"name", re.compile(rf"{rootName}"),lazySearch=True)
    if len(test_root) == 0:
        print("OEM-235: Target root nÃ£o encontrado!")
        return
    #Pega sys
    heads = []
    for searchList in [primSearchList,stbySearchList]:
        parent = None
        headTarget = {}
        for searchTarget in searchList:
            results = get_target_info(searchTarget["reg_pattern_list"],searchTarget["targetType"],searchTarget["unique"])
            for result in results:
                if parent == None:
                    headTarget = result
                    parent = result
                    continue
                parent["children"].append(result)
            if len(results)>0:
                parent = results[0]
            system.extend(results)
        if(headTarget!={}): heads.append(headTarget)
    if len(heads)== 1:
        return [heads[0]]
    if heads[0]["typeName"] == "oracle_dbsys":
        if(heads[1] != {}):
            heads[0]["children"].append(heads[1])
        return [heads[0]]
    if heads[1]["typeName"] == "oracle_dbsys":
        if(heads[0]!= {}):
            heads[1]["children"].append(heads[0])
        return [heads[1]]
    return [heads[0],heads[1]]
