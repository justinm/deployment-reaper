from datetime import timedelta, datetime


def parse_time(t: str):
    return datetime.strptime(t, '%Y-%m-%dT%H:%M:%SZ')


def time_to_str(t: datetime):
    return t.strftime('%Y-%m-%dT%H:%M:%SZ')


def parse_duration(duration: str) -> timedelta:
    num = int(duration[:-1])
    
    if duration.endswith('s'):
        return timedelta(seconds=num)
    elif duration.endswith('m'):
        return timedelta(minutes=num)
    elif duration.endswith('h'):
        return timedelta(hours=num)
    elif duration.endswith('d'):
        return timedelta(days=num)

    raise ValueError('duration is not valid')


def label_selector(obj) -> str:
    output = []

    for key in obj.keys():
        val = str(obj[key])

        if isinstance(obj[key], (bool,)):
            val = val.lower()

        output += ['%s=%s' % (key, val)]

    return ','.join(output)
