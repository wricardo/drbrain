const moneyFormat = new Intl.NumberFormat('en-US', {
	style: 'currency',
	currency: 'USD',
	maximumFractionDigits: 0
});

const countFormat = new Intl.NumberFormat('en-US', {
	maximumFractionDigits: 0
});

export function formatMoney(value: number): string {
	return moneyFormat.format(value);
}

export function formatCount(value: number): string {
	return countFormat.format(Math.round(value));
}

export function formatReputation(value: number): string {
	return value.toFixed(2);
}
