import type { IamResource, IamStatement } from "./types.js";

/**
 * Grants ssm:GetParameter on the specified parameter path pattern.
 */
export function ssmReadPolicy(parameterPattern: IamResource): IamStatement {
  return {
    Effect: "Allow",
    Action: ["ssm:GetParameter", "ssm:GetParameters"],
    Resource: parameterPattern,
  };
}

/**
 * Grants ssm:PutParameter on the specified parameter path pattern.
 */
export function ssmWritePolicy(parameterPattern: IamResource): IamStatement {
  return {
    Effect: "Allow",
    Action: ["ssm:PutParameter"],
    Resource: parameterPattern,
  };
}
