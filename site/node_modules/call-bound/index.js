'use strict';

var GetIntrinsic = require('get-intrinsic');

var callBind = require('call-bind');

// eslint-disable-next-line no-extra-parens
var $indexOf = callBind(/** @type {typeof String.prototype.indexOf} */ (GetIntrinsic('String.prototype.indexOf')));

/** @type {import('.')} */
module.exports = function callBoundIntrinsic(name, allowMissing) {
	// eslint-disable-next-line no-extra-parens
	var intrinsic = /** @type {Parameters<typeof callBind>[0]} */ (GetIntrinsic(name, !!allowMissing));
	if (typeof intrinsic === 'function' && $indexOf(name, '.prototype.') > -1) {
		return callBind(intrinsic);
	}
	return intrinsic;
};
