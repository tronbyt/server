import functools
from flask import (
    Blueprint, flash, g, redirect, render_template, request, session, url_for, current_app
)
from werkzeug.security import generate_password_hash
from werkzeug.utils import secure_filename
import time
import tronbyt_server.db as db
bp = Blueprint('auth', __name__, url_prefix='/auth')

DEBUG=True

def dprint(*args, **kwargs):
    if DEBUG:
        print(*args, **kwargs)
    # else:
    #     logging.info(*args, **kwargs)

@bp.route('/register', methods=('GET', 'POST'))
def register():
    if not current_app.config['TESTING']: time.sleep(2)
    # # only allow admin to register new users
    # if session['username'] != "admin":
    #     return redirect(url_for('manager.index'))
    if request.method == 'POST':
        error = None
        
        username = db.sanitize(secure_filename(request.form['username']))
        if username != request.form['username']:
            error = "Invalid Username"
        password = generate_password_hash(request.form['password'])
        
        if not username:
            error = 'Username is required.'
        elif not password:
            error = 'Password is required.'
        if error != None and db.get_user(username):
            error = 'User is already registered.'   
        if error is None:
            user = dict()
            user["username"] = username
            user["password"] = password
            email = "none"
            if 'email' in request.form:
                if '@' in request.form['email']:
                    email = request.form['email']
            user['email'] = email

            if db.save_user(user,new_user=True):
                db.create_user_dir(username)
                flash(f"Registered as {username}.")
                return redirect(url_for('auth.login'))
            else:
                error = "Couldn't Save User"
        flash(error)
    return render_template('auth/register.html')

@bp.route('/login', methods=('GET', 'POST'))
def login():
    if request.method == 'POST':
        if not current_app.config['TESTING']: time.sleep(2) # slow down brute force attacks
        username = db.sanitize(request.form['username'])
        password = db.sanitize(request.form['password'])
        dprint(f"safeusername : {username} and hp : {password}")
        error = None
        user = db.auth_user(username,password)
        if user is False:
            error = 'Incorrect username/password.'
        if error is None:
            session.clear()
            print("username " + username)
            session['username'] = username
            return redirect(url_for('index'))
        flash(error)
    
    return render_template('auth/login.html')

# edit user info, namely password
@bp.route('/edit', methods=('GET', 'POST'))
def edit():
    if request.method == 'POST':
        username = session['username']
        old_pass = request.form['old_password']
        password = generate_password_hash(request.form['password'])
        error = None
        user = db.auth_user(username,old_pass)
        if user is False:
            error = 'Bad old password.'
        if error is None:
            user['password'] = password
            db.save_user(user)
            flash("Success")
            return redirect(url_for('index'))
        flash(error)
    
    return render_template('auth/edit.html', user=g.user)

@bp.before_app_request
def load_logged_in_user():
    username = session.get('username')
    if username is None:
        g.user = None
    else:
        g.user = db.get_user(username) # will return none if non existant
        

@bp.route('/logout')
def logout():
    session.clear()
    flash("Logged Out")
    return redirect(url_for('auth.login'))

def login_required(view):
    @functools.wraps(view)
    def wrapped_view(**kwargs):
        if g.user is None:
            return redirect(url_for('auth.login'))
        return view(**kwargs)
    return wrapped_view