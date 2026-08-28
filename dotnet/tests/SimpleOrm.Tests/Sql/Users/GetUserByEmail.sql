select id, name, email, created_at, updated_at
from users
where email = @Email
