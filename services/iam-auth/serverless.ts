import type { AWS } from '@serverless/typescript';
import { DEPLOYMENT_BUCKET, REGION, STAGE } from '../../common/custom-parameters';
import { buildResourceTags } from '../../common/aws-service-tags';

const SERVICE_NAME = 'iam-auth';
const tags = buildResourceTags(SERVICE_NAME);

const config: AWS = {
  service: SERVICE_NAME,
  frameworkVersion: '4',
  useDotenv: true,

  package: {
    individually: true,
    patterns: ['!./**'],
  },

  provider: {
    name: 'aws',
    runtime: 'provided.al2023',
    architecture: 'arm64',
    region: REGION,
    stage: STAGE,
    timeout: 10,
    memorySize: 128,
    logRetentionInDays: 90,
    tags,
    stackTags: tags,
    deploymentBucket: { name: DEPLOYMENT_BUCKET },
    tracing: {
      lambda: true,
    },
    environment: {
      APP_NAME: SERVICE_NAME,
      APP_STAGE: '${self:provider.stage}',
      APP_REGION: '${self:provider.region}',
      APP_MODE: '${env:APP_MODE, "lambda"}',
      LOG_LEVEL: '${env:LOG_LEVEL, "info"}',
      JWT_SECRET: '${ssm:/indomito/${self:provider.stage}/auth/jwt-secret}',
    },
  },

  functions: {
    authorize: {
      handler: 'bootstrap',
      description: 'Lambda Authorizer para HTTP API Gateway v2',
      timeout: 10,
      memorySize: 128,
      package: {
        artifact: '.serverless-artifacts/authorize-v1.zip',
      },
    },
  },

  resources: {
    Outputs: {
      iamAuthArn: {
        Description: 'ARN de la funcion Lambda Authorizer',
        Value: { 'Fn::GetAtt': ['AuthorizeLambdaFunction', 'Arn'] },
        Export: { Name: '${self:service}-${self:provider.stage}-iamAuthArn' },
      },
    },
  } as AWS['resources'],
};

module.exports = config;
