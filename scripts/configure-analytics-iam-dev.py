#!/usr/bin/env python3
"""Configura únicamente Analítica para ADM en el ambiente elegido; DEV por defecto."""
import argparse
import datetime
import json
import subprocess


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--account-id', required=True)
    parser.add_argument('--stage', choices=['dev', 'prd'], default='dev')
    parser.add_argument('--apply', action='store_true', help='Sin esta opción solo prepara y muestra el cambio')
    args = parser.parse_args()
    base = ['aws', '--profile', 'pa-' + args.stage, '--region', 'us-east-1', '--output', 'json', '--no-cli-pager']

    def aws(*parts):
        raw = subprocess.check_output(base + list(parts), text=True)
        return json.loads(raw) if raw.strip() else {}

    identity = aws('sts', 'get-caller-identity')
    if identity['Account'] != args.account_id:
        raise SystemExit('La cuenta AWS no coincide con la esperada')
    stack = aws('cloudformation', 'describe-stacks', '--stack-name', 'indomito-hub-infra-ddb-' + args.stage)
    table = next(x['OutputValue'] for x in stack['Stacks'][0]['Outputs'] if x['OutputKey'] == 'UsuariosTableName')
    def encode(value):
        if isinstance(value, bool): return {'BOOL': value}
        if isinstance(value, int): return {'N': str(value)}
        if isinstance(value, list): return {'L': [encode(x) for x in value]}
        return {'S': value}
    def item(value): return {k: encode(v) for k, v in value.items()}
    def get(pk, sk):
        return aws('dynamodb', 'get-item', '--table-name', table, '--consistent-read', '--key', json.dumps(item({'pk':pk,'sk':sk}))).get('Item')
    app, permissions = 'APP#INDOMITO_HUB', 'APP#PROFILE#PERMISSIONS'
    profile = get(permissions, 'PFL#ADM#METADATA')
    if not profile or profile.get('code', {}).get('S') != 'ADM':
        raise SystemExit('No existe el perfil exacto ADM; no se crea ni sustituye por ADMIN')
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    records = [
        {'pk':app,'sk':app+'#LV1#ANALYTICS','code':'ANALYTICS','title':'Analítica','category':'Analítica','path':'','icon':'icon-[tabler--chart-bar]','order':80,'active':True,'level':'LV1','endpoints':[],'createdAt':now},
        {'pk':app,'sk':app+'#LV1#ANALYTICS#LV2#ANALYTICS_BALANCE_DETAIL','code':'ANALYTICS_BALANCE_DETAIL','title':'Detalle balance','category':'Analítica','path':'/analitica/detalle-balance','icon':'icon-[tabler--chart-bar]','order':1,'active':True,'level':'LV2','parentCode':'ANALYTICS','endpoints':['/analitica'],'createdAt':now},
        {'pk':permissions,'sk':'PFL#ADM#'+app+'#LV1#ANALYTICS','moduleCode':'ANALYTICS','allowances':['r']},
        {'pk':permissions,'sk':'PFL#ADM#'+app+'#LV1#ANALYTICS#LV2#ANALYTICS_BALANCE_DETAIL','moduleCode':'ANALYTICS_BALANCE_DETAIL','parentCode':'ANALYTICS','allowances':['r','u']},
    ]
    writes = []
    for record in records:
        wanted = item(record)
        existing = get(record['pk'], record['sk'])
        if existing:
            # Conserva orden visual, atributos adicionales y concesiones administrativas más amplias.
            keys = set(wanted) - {'createdAt', 'order', 'allowances', 'endpoints'}
            if any(existing.get(k) != wanted[k] for k in keys):
                raise SystemExit('Registro existente diferente: '+record['sk']+'. Revisar sin sobrescribir.')
            for field in ('allowances', 'endpoints'):
                if field in wanted:
                    required = {entry['S'] for entry in wanted[field]['L']}
                    granted = {entry['S'] for entry in existing.get(field, {}).get('L', [])}
                    if not required.issubset(granted):
                        raise SystemExit('Faltan concesiones en '+record['sk']+'. Revisar sin sobrescribir.')
            continue
        writes.append({'Put':{'TableName':table,'Item':wanted,'ConditionExpression':'attribute_not_exists(pk)'}})
    print(json.dumps({'account':args.account_id,'stage':args.stage,'table':table,'changes':writes}, ensure_ascii=False, indent=2))
    if args.apply and writes:
        aws('dynamodb','transact-write-items','--transact-items',json.dumps(writes))
        for record in records:
            if not get(record['pk'],record['sk']): raise SystemExit('Falló verificación final')
        print('Analítica configurada para ADM. Renovar permisos de la sesión.')

if __name__ == '__main__':
    main()
