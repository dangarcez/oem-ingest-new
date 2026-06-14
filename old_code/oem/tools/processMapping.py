import oem.tools.oemapping as oemapping


def get_tagged_targets_list(system: list) -> list:
    prepared_targets_list = []
    prepared_targets_list_id = set()
    #---------------Prepara targets
    adjacentTargets = ("host","oracle_listener")

    def tagged_target(target: dict, tags: dict):
        target["tags"] = {}

        if target["id"] in prepared_targets_list_id:
            return []
        prepared_targets_list_id.add(target["id"])

        
        if target["typeName"] not in adjacentTargets:
            target["tags"].update(tags)
        target["tags"].update({"target_name":target["name"]})
        target["tags"].update({"target_type":target["typeName"]})
        target["tags"].update({target["typeName"]:target["name"]})
        target["tags"].update({"dg_role":target["dg_role"]}) if "dg_role" in target else None
        target["tags"].update({"target_name":target["name"].split('.')[0]}) if target["typeName"] == "host" else None
        target["tags"].update({target["typeName"]:target["name"].split('.')[0]}) if target["typeName"] == "host" else None
        target["tags"].update({"target_name":target["name"].replace("LISTENER_","").split('.')[0]+ "_lstnr"}) if target["typeName"] == "oracle_listener" else None
        target["tags"].update({target["typeName"]:target["name"].replace("LISTENER_","").split('.')[0]+ "_lstnr"}) if target["typeName"] == "oracle_listener" else None
        


        if "machine_name" in target:
            target["tags"]["machine_name"] = target["machine_name"].split('.')[0]
        if "listener_name" in target:
            target["tags"]["listener_name"] = target["listener_name"].replace("LISTENER_","").split('.')[0] + "_lstnr"

        lista = [target]
        for child in target["children"]:
            lista= lista + tagged_target(child,target["tags"])
        del target["children"]
        return lista

    for target in system:
        prepared_targets_list += tagged_target(target,{})
    return prepared_targets_list