import callBind from 'call-bind-apply-helpers';

declare function callBoundIntrinsic(name: string, allowMissing?: boolean): ReturnType<typeof callBind>;

export = callBoundIntrinsic;