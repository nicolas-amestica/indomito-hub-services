import { ApiServices } from '../../common/api-services';
import { buildGoServiceServerless } from '../../common/go-service';

const endpoints = [
  { name: 'hello-world-v1', method: 'POST', path: '/v1/hello', timeout: 15, public: true },
] as const;

const config = buildGoServiceServerless(ApiServices.Auth, [...endpoints]);

module.exports = config;
